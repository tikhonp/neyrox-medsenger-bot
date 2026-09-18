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

	// --- Handled separately in appendSleep (see sleep.go) ---
	// hypnogram is not a scalar: it maps to the six time_interval categories (in_bed,
	// awake, asleep_core, asleep_deep, asleep_REM, asleep_unspecified).

	// --- Handled separately in appendSleepSummaries (see sleep_summary.go) ---
	// sleep is not a scalar either: its rows are running per-stage totals in hours,
	// re-sent every ~15 min, not measurements of an instant. For a night that has no
	// hypnogram they are synthesized into the same time_interval categories.

	// --- No matching Medsenger category yet (register one, then uncomment) ---
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
	{NeyroxMetric: "inflammation", MedsengerCategory: "neyrox_inflammation"},
	// {NeyroxMetric: "neuroplasticity", MedsengerCategory: ""},
	// {NeyroxMetric: "formindicators", MedsengerCategory: ""},          // form/survey data, nullable date_device
}

type Syncer struct {
	db    db.ModelsFactory
	maigo *maigo.Client
	nc    *neyroxclient.Client

	// indicators maps a type_indicator UUID to its lower-cased name, loaded once from
	// Neyrox's typeindicators reference table; blood pressure resolves its two
	// categories through it. RunOnce runs in a single goroutine, so no lock is needed.
	indicators       map[string]string
	indicatorsLoaded bool
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

	// bloodPressureMetric is the Neyrox endpoint, and watermark key, for blood pressure.
	bloodPressureMetric = "bloodpressure"
)

func (s *Syncer) syncAccount(acc *models.NeyroxAccount) error {
	log.Printf("Syncing Neyrox data for contract %d", acc.ContractID)

	access, err := s.ensureAccessToken(acc)
	if err != nil {
		return err
	}

	watermarks, err := s.loadWatermarks(acc)
	if err != nil {
		return err
	}

	var records []maigo.Record
	// advanced holds the new watermark of every metric that produced records. Nothing
	// is persisted until AddRecords has succeeded, so a failed push is simply retried.
	advanced := make(map[string]sql.NullTime)

	// Simple metrics: one Neyrox value -> one fixed Medsenger category.
	for _, m := range syncedMetrics {
		category := m.MedsengerCategory
		newest, err := s.appendMetric(access, watermarks[m.NeyroxMetric], m.NeyroxMetric,
			func(neyroxclient.Measurement) (string, bool) { return category, true }, &records)
		if err != nil {
			return s.handleFetchErr(acc, m.NeyroxMetric, err)
		}
		if newest.Valid {
			advanced[m.NeyroxMetric] = newest
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
		newest, err := s.appendMetric(access, watermarks[bloodPressureMetric], bloodPressureMetric, bpFn, &records)
		if err != nil {
			return s.handleFetchErr(acc, bloodPressureMetric, err)
		}
		if newest.Valid {
			advanced[bloodPressureMetric] = newest
		}
	}

	// Sleep: stage intervals, pushed to the six time_interval categories.
	newest, metric, err := s.appendSleep(acc, access, watermarks[hypnogramMetric], &records)
	if err != nil {
		return s.handleFetchErr(acc, metric, err)
	}
	if newest.Valid {
		advanced[hypnogramMetric] = newest
	}

	// Sleep summaries: the same categories, synthesized for nights without a hypnogram.
	newest, metric, err = s.appendSleepSummaries(acc, access, watermarks[sleepSummaryMetric], &records)
	if err != nil {
		return s.handleFetchErr(acc, metric, err)
	}
	if newest.Valid {
		advanced[sleepSummaryMetric] = newest
	}

	if len(records) > 0 {
		log.Printf("Pushing %d records to Medsenger for contract %d", len(records), acc.ContractID)
		if _, err := s.maigo.AddRecords(acc.ContractID, records); err != nil {
			return fmt.Errorf("add records: %w", err)
		}
	}
	// A watermark can move without records: a sleep session a hypnogram already covers
	// is skipped, not pushed, and must still be left behind.
	if len(advanced) > 0 {
		if err := s.saveWatermarks(acc, advanced); err != nil {
			return err
		}
	}

	s.sendSuccessMessage(acc)
	return nil
}

// loadWatermarks returns the per-metric watermark of every metric this syncer pushes.
//
// A metric with no row yet is seeded from the account's global LastSync and persisted
// straight away. Persisting matters: LastSync keeps tracking the newest record of any
// metric, so a metric that yields nothing on its first cycles (sleep, on day one) would
// otherwise have its floor dragged forward with it and would never sync at all.
func (s *Syncer) loadWatermarks(acc *models.NeyroxAccount) (map[string]sql.NullTime, error) {
	watermarks, err := s.db.MetricSyncs().GetByContractID(acc.ContractID)
	if err != nil {
		return nil, fmt.Errorf("get watermarks: %w", err)
	}
	for _, metric := range syncedMetricNames() {
		if _, ok := watermarks[metric]; ok {
			continue
		}
		if err := s.db.MetricSyncs().Set(acc.ContractID, metric, acc.LastSync); err != nil {
			return nil, fmt.Errorf("seed watermark %s: %w", metric, err)
		}
		watermarks[metric] = acc.LastSync
	}
	return watermarks, nil
}

// saveWatermarks persists the metrics that advanced and keeps the account's global
// LastSync at the newest record of any metric, which is the floor a metric added later
// starts from.
func (s *Syncer) saveWatermarks(acc *models.NeyroxAccount, advanced map[string]sql.NullTime) error {
	for metric, t := range advanced {
		if err := s.db.MetricSyncs().Set(acc.ContractID, metric, t); err != nil {
			return fmt.Errorf("save watermark %s: %w", metric, err)
		}
		if !acc.LastSync.Valid || t.Time.After(acc.LastSync.Time) {
			acc.LastSync = t
		}
	}
	return s.db.NeyroxAccounts().Save(acc)
}

// syncedMetricNames lists every watermark key.
func syncedMetricNames() []string {
	names := make([]string, 0, len(syncedMetrics)+3)
	for _, m := range syncedMetrics {
		names = append(names, m.NeyroxMetric)
	}
	return append(names, bloodPressureMetric, hypnogramMetric, sleepSummaryMetric)
}

// sinceTime converts a watermark to the client's optional filter argument.
func sinceTime(watermark sql.NullTime) *time.Time {
	if !watermark.Valid {
		return nil
	}
	return &watermark.Time
}

// appendMetric fetches one Neyrox metric and appends each new, non-null measurement
// as a Medsenger record. categoryFn picks the category per measurement (returning
// false skips it). It returns the latest date_device appended, i.e. the metric's new
// watermark, which is invalid when nothing was appended.
func (s *Syncer) appendMetric(
	access string, watermark sql.NullTime, metric string,
	categoryFn func(neyroxclient.Measurement) (string, bool),
	records *[]maigo.Record,
) (sql.NullTime, error) {
	var newest sql.NullTime
	measurements, err := s.nc.FetchMeasurements(access, metric, sinceTime(watermark))
	if err != nil {
		return newest, err
	}
	for _, meas := range measurements {
		if meas.Value == nil {
			continue
		}
		if watermark.Valid && !meas.DateDevice.After(watermark.Time) {
			continue
		}
		category, ok := categoryFn(meas)
		if !ok {
			continue
		}
		*records = append(*records, maigo.NewRecord(category, *meas.Value, meas.DateDevice))
		if !newest.Valid || meas.DateDevice.After(newest.Time) {
			newest = sql.NullTime{Valid: true, Time: meas.DateDevice}
		}
	}
	return newest, nil
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

// loadIndicators fills s.indicators (type_indicator UUID -> lower-cased name) from the
// Neyrox typeindicators reference table. It is small reference data, fetched once per
// process, and is what lets a measurement's type_indicator be read as a human label.
func (s *Syncer) loadIndicators(access string) error {
	if s.indicatorsLoaded {
		return nil
	}
	indicators, err := s.nc.FetchTypeIndicators(access)
	if err != nil {
		return err
	}
	s.indicators = make(map[string]string, len(indicators))
	for _, ind := range indicators {
		if ind.Name == nil {
			continue
		}
		s.indicators[ind.ID] = strings.ToLower(*ind.Name)
	}
	s.indicatorsLoaded = true
	return nil
}

// indicatorMatching returns the UUID of the first indicator whose name contains one of
// substrs, or "" when none does.
func (s *Syncer) indicatorMatching(substrs ...string) string {
	for id, name := range s.indicators {
		for _, sub := range substrs {
			if strings.Contains(name, sub) {
				return id
			}
		}
	}
	return ""
}

// bpCategoryFn returns a resolver mapping a bloodpressure measurement to the
// systolic_pressure / diastolic_pressure category by its type_indicator.
//
// Neyrox tags each BP value via type_indicator, a reference into the typeindicators
// table; the two UUIDs are looked up by name (the systolic row's name contains
// "систол", the diastolic one "диастол"). Returns (nil, nil) when neither can be
// resolved, so blood pressure is simply skipped.
func (s *Syncer) bpCategoryFn(access string) (func(neyroxclient.Measurement) (string, bool), error) {
	if err := s.loadIndicators(access); err != nil {
		return nil, err
	}
	sys := s.indicatorMatching("систол", "systol")
	dia := s.indicatorMatching("диастол", "diastol")
	if sys == "" || dia == "" {
		log.Printf("Neyrox: blood pressure indicators not fully resolved (systolic=%q diastolic=%q)", sys, dia)
	}
	if sys == "" && dia == "" {
		return nil, nil
	}
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
