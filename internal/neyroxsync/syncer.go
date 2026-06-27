// Package neyroxsync orchestrates pulling measurements from Neyrox and pushing
// them into Medsenger as records. It is driven by the worker (cmd/worker).
package neyroxsync

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db/models"
	neyroxclient "github.com/tikhonp/medsenger-neyrox-bot/internal/util/neyrox_client"
)

// metricMapping maps a Neyrox metric endpoint to a Medsenger record category.
//
// The MedsengerCategory slugs are taken verbatim from the clinic's agent category
// catalog — a category not in that catalog is dropped by maigo.AddRecords, so only
// metrics with a real category are active. Mind the platform's exact spellings:
// "glukose" (not glucose), "respiration_rate" (not respiratory_rate), and calories
// burned is "active_energy_burned".
//
// Everything commented out below has NO matching Medsenger category yet. To enable
// one, register a category in the agent config and uncomment the line with that
// slug. Resources with a non-scalar shape (ecg, dataecg, datarrseries, hypnogram)
// are excluded entirely — they need bespoke handling, not a single value push.
type metricMapping struct {
	NeyroxMetric      string
	MedsengerCategory string
}

var syncedMetrics = []metricMapping{
	// --- Active: mapped to confirmed Medsenger categories (verified against the live API) ---
	{NeyroxMetric: "pulse", MedsengerCategory: "pulse"},
	{NeyroxMetric: "oxygenation", MedsengerCategory: "spo2"},
	{NeyroxMetric: "respiratoryrate", MedsengerCategory: "respiration_rate"},
	// NOTE: the band reports skin temperature (~35 °C), not core body temp — Medsenger's
	// "temperature" is body temp, so a clinician may misread it. Confirm this is acceptable.
	{NeyroxMetric: "temperature", MedsengerCategory: "temperature"},
	{NeyroxMetric: "glucose", MedsengerCategory: "glukose"},
	{NeyroxMetric: "steps", MedsengerCategory: "steps"},
	{NeyroxMetric: "calories", MedsengerCategory: "active_energy_burned"},
	{NeyroxMetric: "stress", MedsengerCategory: "stress"},

	// --- Handled separately in syncAccount (see bpCategoryFn) ---
	// bloodpressure carries one `value` tagged systolic/diastolic via type_indicator,
	// so it maps to two Medsenger categories and can't be a single static entry.

	// --- No matching Medsenger category yet (register one, then uncomment) ---
	// sleep: endpoint mixes deep ("Глубокий сон") + light ("Поверхностный сон") stage
	// durations by type_indicator — not a single value, and Medsenger's "sleep" is a
	// quality score ("Качество сна"). Needs per-stage categories + a BP-style split.
	// {NeyroxMetric: "sleep", MedsengerCategory: ""},
	// {NeyroxMetric: "averagepulse", MedsengerCategory: ""},            // only "pulse" (resting) exists
	// {NeyroxMetric: "heartratevariability", MedsengerCategory: ""},    // no HRV category
	// {NeyroxMetric: "heartratevariabilityecg", MedsengerCategory: ""}, // no HRV category
	// {NeyroxMetric: "hrvsnapshot", MedsengerCategory: ""},             // no HRV category (value is RMSSD)
	// {NeyroxMetric: "baevskysi", MedsengerCategory: ""},
	// {NeyroxMetric: "qtinterval", MedsengerCategory: ""},
	// {NeyroxMetric: "neurocalories", MedsengerCategory: ""},
	// {NeyroxMetric: "metabolism", MedsengerCategory: ""},
	// {NeyroxMetric: "activitystatus", MedsengerCategory: ""},          // "activity" is duration (min), not a 0-4 level
	// {NeyroxMetric: "movement", MedsengerCategory: ""},
	// {NeyroxMetric: "intensity", MedsengerCategory: ""},
	// {NeyroxMetric: "vo2max", MedsengerCategory: ""},
	// {NeyroxMetric: "emotionalbalance", MedsengerCategory: ""},        // Medsenger has emotional_instability (opposite concept)
	// {NeyroxMetric: "electrodermalactivity", MedsengerCategory: ""},
	// {NeyroxMetric: "edaz1", MedsengerCategory: ""},
	// {NeyroxMetric: "edaz2", MedsengerCategory: ""},
	// {NeyroxMetric: "vitality", MedsengerCategory: ""},
	// {NeyroxMetric: "functionalage", MedsengerCategory: ""},
	// {NeyroxMetric: "adoptability", MedsengerCategory: ""},
	// {NeyroxMetric: "inflammation", MedsengerCategory: ""},
	// {NeyroxMetric: "neuroplasticity", MedsengerCategory: ""},
	// {NeyroxMetric: "formindicators", MedsengerCategory: ""},          // form/survey data, nullable date_device
}

type Syncer struct {
	db    db.ModelsFactory
	maigo *maigo.Client
	nc    *neyroxclient.Client

	// Blood-pressure type_indicator UUIDs, resolved once from Neyrox's typeindicators
	// reference table. RunOnce runs in a single goroutine, so no lock is needed.
	bpResolved  bool
	bpSystolic  string
	bpDiastolic string
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

// bpSystolicCategory / bpDiastolicCategory are the Medsenger categories a Neyrox
// bloodpressure record maps to, chosen per-record by its type_indicator.
const (
	bpSystolicCategory  = "systolic_pressure"
	bpDiastolicCategory = "diastolic_pressure"
)

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

	// Simple metrics: one Neyrox value -> one fixed Medsenger category.
	for _, m := range syncedMetrics {
		category := m.MedsengerCategory
		err := s.appendMetric(acc, access, since, m.NeyroxMetric,
			func(neyroxclient.Measurement) (string, bool) { return category, true },
			&records, &newest)
		if err != nil {
			return s.handleFetchErr(acc, m.NeyroxMetric, err)
		}
	}

	// Blood pressure: each record holds a single value tagged systolic or diastolic
	// via type_indicator, which Medsenger stores as two separate categories.
	bpFn, err := s.bpCategoryFn(access)
	if errors.Is(err, neyroxclient.ErrUnauthorized) {
		return s.handleFetchErr(acc, "typeindicators", err)
	}
	if err != nil {
		// Reference-table lookup failed: skip BP this cycle, don't abort the rest.
		log.Printf("resolve blood pressure indicators for contract %d: %v", acc.ContractID, err)
	} else if bpFn != nil {
		if err := s.appendMetric(acc, access, since, "bloodpressure", bpFn, &records, &newest); err != nil {
			return s.handleFetchErr(acc, "bloodpressure", err)
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

// appendMetric fetches one Neyrox metric and appends each new, non-null measurement
// as a Medsenger record. categoryFn picks the category per measurement (returning
// false skips it); newest advances to the latest date_device appended.
func (s *Syncer) appendMetric(
	acc *models.NeyroxAccount, access string, since *time.Time, metric string,
	categoryFn func(neyroxclient.Measurement) (string, bool),
	records *[]maigo.Record, newest *sql.NullTime,
) error {
	measurements, err := s.nc.FetchMeasurements(access, metric, since)
	if err != nil {
		return err
	}
	for _, meas := range measurements {
		if meas.Value == nil {
			continue
		}
		if acc.LastSync.Valid && !meas.DateDevice.After(acc.LastSync.Time) {
			continue
		}
		category, ok := categoryFn(meas)
		if !ok {
			continue
		}
		*records = append(*records, maigo.NewRecord(category, *meas.Value, meas.DateDevice))
		if !newest.Valid || meas.DateDevice.After(newest.Time) {
			*newest = sql.NullTime{Valid: true, Time: meas.DateDevice}
		}
	}
	return nil
}

// handleFetchErr maps a fetch/resolve error to syncAccount's return value: an expired
// token is cleared so the next run re-authenticates; any other error notifies the
// patient once. It always returns a non-nil error (the account sync is aborted).
func (s *Syncer) handleFetchErr(acc *models.NeyroxAccount, metric string, err error) error {
	if errors.Is(err, neyroxclient.ErrUnauthorized) {
		// Token expired mid-cycle: clear it so the next run re-authenticates.
		acc.AccessToken = sql.NullString{}
		if saveErr := s.db.NeyroxAccounts().Save(acc); saveErr != nil {
			return saveErr
		}
		return err
	}
	s.sendErrMessage(acc, "Ошибка синхронизации с Neyrox. Попробуем ещё раз позже.")
	return fmt.Errorf("fetch %s: %w", metric, err)
}

// bpCategoryFn returns a resolver mapping a bloodpressure measurement to the
// systolic_pressure / diastolic_pressure category by its type_indicator.
//
// Neyrox tags each BP value via type_indicator, a reference into the typeindicators
// table; the two UUIDs are looked up once by name (the systolic row's name contains
// "систол", the diastolic one "диастол") and cached for the process. Returns
// (nil, nil) when neither can be resolved, so blood pressure is simply skipped.
func (s *Syncer) bpCategoryFn(access string) (func(neyroxclient.Measurement) (string, bool), error) {
	if !s.bpResolved {
		indicators, err := s.nc.FetchTypeIndicators(access)
		if err != nil {
			return nil, err
		}
		for _, ind := range indicators {
			if ind.Name == nil {
				continue
			}
			switch name := strings.ToLower(*ind.Name); {
			case strings.Contains(name, "систол") || strings.Contains(name, "systol"):
				s.bpSystolic = ind.ID
			case strings.Contains(name, "диастол") || strings.Contains(name, "diastol"):
				s.bpDiastolic = ind.ID
			}
		}
		s.bpResolved = true
		if s.bpSystolic == "" || s.bpDiastolic == "" {
			log.Printf("Neyrox: blood pressure indicators not fully resolved (systolic=%q diastolic=%q)",
				s.bpSystolic, s.bpDiastolic)
		}
	}
	if s.bpSystolic == "" && s.bpDiastolic == "" {
		return nil, nil
	}
	sys, dia := s.bpSystolic, s.bpDiastolic
	return func(m neyroxclient.Measurement) (string, bool) {
		switch {
		case sys != "" && m.TypeIndicator == sys:
			return bpSystolicCategory, true
		case dia != "" && m.TypeIndicator == dia:
			return bpDiastolicCategory, true
		default:
			return "", false
		}
	}, nil
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
