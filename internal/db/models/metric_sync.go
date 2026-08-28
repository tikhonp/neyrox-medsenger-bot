package models

import (
	"database/sql"

	"github.com/jmoiron/sqlx"
)

// MetricSync is the per-metric sync watermark for one contract: the date_device of
// the newest measurement of that metric already pushed to Medsenger.
type MetricSync struct {
	ContractID int          `db:"contract_id"`
	Metric     string       `db:"metric"`
	LastSync   sql.NullTime `db:"last_sync"`
}

type MetricSyncs interface {
	// GetByContractID returns the watermark of every metric synced for a contract,
	// keyed by metric name. Metrics never synced are absent from the map.
	GetByContractID(contractID int) (map[string]sql.NullTime, error)

	// Set upserts one metric's watermark.
	Set(contractID int, metric string, lastSync sql.NullTime) error

	// DeleteByContractID drops every watermark of a contract.
	DeleteByContractID(contractID int) error
}

type metricSyncs struct {
	db *sqlx.DB
}

func NewMetricSyncs(db *sqlx.DB) MetricSyncs {
	return &metricSyncs{db: db}
}

func (m *metricSyncs) GetByContractID(contractID int) (map[string]sql.NullTime, error) {
	var rows []MetricSync
	const query = `SELECT * FROM neyrox_metric_sync WHERE contract_id = $1`
	if err := m.db.Select(&rows, query, contractID); err != nil {
		return nil, err
	}
	watermarks := make(map[string]sql.NullTime, len(rows))
	for _, r := range rows {
		watermarks[r.Metric] = r.LastSync
	}
	return watermarks, nil
}

func (m *metricSyncs) Set(contractID int, metric string, lastSync sql.NullTime) error {
	const query = `
		INSERT INTO neyrox_metric_sync (contract_id, metric, last_sync)
		VALUES ($1, $2, $3) ON CONFLICT (contract_id, metric)
		DO UPDATE SET last_sync = EXCLUDED.last_sync
	`
	_, err := m.db.Exec(query, contractID, metric, lastSync)
	return err
}

func (m *metricSyncs) DeleteByContractID(contractID int) error {
	_, err := m.db.Exec(`DELETE FROM neyrox_metric_sync WHERE contract_id = $1`, contractID)
	return err
}
