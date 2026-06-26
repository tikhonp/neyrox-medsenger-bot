// Package neyroxsync orchestrates pulling measurements from Neyrox and pushing
// them into Medsenger as records. It is driven by the worker (cmd/worker).
package neyroxsync

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db/models"
	neyroxclient "github.com/tikhonp/medsenger-neyrox-bot/internal/util/neyrox_client"
)

// metricMapping maps a Neyrox metric endpoint to a Medsenger record category.
// Extend this list as more smart-band metrics are wired up. The Medsenger
// category names below are placeholders — confirm them against the clinic's agent config.
type metricMapping struct {
	NeyroxMetric      string
	MedsengerCategory string
}

var syncedMetrics = []metricMapping{
	{NeyroxMetric: "pulse", MedsengerCategory: "pulse"},
	// {NeyroxMetric: "bloodpressure", MedsengerCategory: "blood_pressure"},
	// {NeyroxMetric: "oxygenation", MedsengerCategory: "spo2"},
	// {NeyroxMetric: "steps", MedsengerCategory: "steps"},
	// {NeyroxMetric: "temperature", MedsengerCategory: "temperature"},
}

type Syncer struct {
	db    db.ModelsFactory
	maigo *maigo.Client
	nc    *neyroxclient.Client
}

func New(database db.ModelsFactory, mc *maigo.Client, nc *neyroxclient.Client) *Syncer {
	return &Syncer{db: database, maigo: mc, nc: nc}
}

// RunOnce syncs every active contract's connected Neyrox account. Per-account
// failures are logged and do not abort the cycle.
func (s *Syncer) RunOnce() error {
	accounts, err := s.db.NeyroxAccounts().GetActiveToSync()
	if err != nil {
		return fmt.Errorf("get active accounts: %w", err)
	}
	for i := range accounts {
		if err := s.syncAccount(&accounts[i]); err != nil {
			log.Printf("sync contract %d: %v", accounts[i].ContractID, err)
		}
	}
	return nil
}

func (s *Syncer) syncAccount(acc *models.NeyroxAccount) error {
	log.Printf("Syncing Neyrox data for contract %d", acc.ContractID)

	access, err := s.ensureAccessToken(acc)
	if err != nil {
		return err
	}

	var since *time.Time
	if acc.LastSync.Valid {
		since = &acc.LastSync.Time
	}

	var records []maigo.Record
	newest := acc.LastSync
	for _, m := range syncedMetrics {
		measurements, err := s.nc.FetchMeasurements(access, m.NeyroxMetric, since)
		if errors.Is(err, neyroxclient.ErrUnauthorized) {
			// Token expired mid-cycle: clear it so the next run re-authenticates.
			acc.AccessToken = sql.NullString{}
			if saveErr := s.db.NeyroxAccounts().Save(acc); saveErr != nil {
				return saveErr
			}
			return err
		}
		if err != nil {
			s.sendErrMessage(acc, "Ошибка синхронизации с Neyrox. Попробуем ещё раз позже.")
			return fmt.Errorf("fetch %s: %w", m.NeyroxMetric, err)
		}
		for _, meas := range measurements {
			if meas.Value == nil {
				continue
			}
			if acc.LastSync.Valid && !meas.DateDevice.After(acc.LastSync.Time) {
				continue
			}
			records = append(records, maigo.NewRecord(m.MedsengerCategory, *meas.Value, meas.DateDevice))
			if !newest.Valid || meas.DateDevice.After(newest.Time) {
				newest = sql.NullTime{Valid: true, Time: meas.DateDevice}
			}
		}
	}

	if len(records) > 0 {
		log.Printf("Pushing %d records to Medsenger for contract %d", len(records), acc.ContractID)
		if _, err := s.maigo.AddRecords(acc.ContractID, records); err != nil {
			return fmt.Errorf("add records: %w", err)
		}
		acc.LastSync = newest
		if err := s.db.NeyroxAccounts().Save(acc); err != nil {
			return err
		}
	}

	s.sendSuccessMessage(acc)
	return nil
}

// ensureAccessToken returns a usable access token, refreshing or logging in as
// needed, and persists any new tokens to the account.
func (s *Syncer) ensureAccessToken(acc *models.NeyroxAccount) (string, error) {
	if acc.AccessToken.Valid && acc.AccessToken.String != "" {
		return acc.AccessToken.String, nil
	}
	if acc.RefreshToken.Valid && acc.RefreshToken.String != "" {
		access, err := s.nc.Refresh(acc.RefreshToken.String)
		if err == nil {
			acc.AccessToken = sql.NullString{Valid: true, String: access}
			return access, s.db.NeyroxAccounts().Save(acc)
		}
		// Refresh failed (expired/rotated) — fall through to a fresh login.
	}
	return s.login(acc)
}

func (s *Syncer) login(acc *models.NeyroxAccount) (string, error) {
	tp, err := s.nc.Login(acc.Email, acc.Password)
	if err != nil {
		if errors.Is(err, neyroxclient.ErrInvalidCredentials) {
			s.sendErrMessage(acc, "Не удалось войти в аккаунт Neyrox: проверьте логин и пароль в настройках агента.")
		}
		return "", fmt.Errorf("login: %w", err)
	}
	acc.AccessToken = sql.NullString{Valid: true, String: tp.Access}
	acc.RefreshToken = sql.NullString{Valid: true, String: tp.Refresh}
	if err := s.db.NeyroxAccounts().Save(acc); err != nil {
		return "", err
	}
	return tp.Access, nil
}

// sendErrMessage sends one urgent message per failure transition (gated on SyncErrMsgReady).
func (s *Syncer) sendErrMessage(acc *models.NeyroxAccount, text string) {
	if !acc.SyncErrMsgReady {
		return
	}
	if _, err := s.maigo.SendMessage(acc.ContractID, text, maigo.Urgent()); err != nil {
		log.Printf("send err message for contract %d: %v", acc.ContractID, err)
		return
	}
	acc.SyncErrMsgReady = false
	acc.SyncSuccessMsgSent = false
	if err := s.db.NeyroxAccounts().Save(acc); err != nil {
		log.Printf("save after err message for contract %d: %v", acc.ContractID, err)
	}
}

// sendSuccessMessage sends the "sync configured" message exactly once (gated on SyncSuccessMsgSent).
func (s *Syncer) sendSuccessMessage(acc *models.NeyroxAccount) {
	if acc.SyncSuccessMsgSent {
		return
	}
	if _, err := s.maigo.SendMessage(acc.ContractID, "Синхронизация с умным браслетом Neyrox успешно настроена."); err != nil {
		log.Printf("send success message for contract %d: %v", acc.ContractID, err)
		return
	}
	acc.SyncErrMsgReady = true
	acc.SyncSuccessMsgSent = true
	if err := s.db.NeyroxAccounts().Save(acc); err != nil {
		log.Printf("save after success message for contract %d: %v", acc.ContractID, err)
	}
}
