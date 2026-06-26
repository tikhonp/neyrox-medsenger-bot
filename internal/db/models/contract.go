// Package models contains the data models and interfaces for the Neyrox bot.
package models

import (
	"database/sql"

	"github.com/jmoiron/sqlx"
)

// Contract represents a Medsenger contract.
// Created on agent /init and persisted during the agent lifecycle.
type Contract struct {
	ID           int            `db:"id"`
	IsActive     bool           `db:"is_active"`
	ClinicID     int            `db:"clinic_id"`
	Locale       string         `db:"locale"`
	PatientName  sql.NullString `db:"patient_name"`
	PatientEmail sql.NullString `db:"patient_email"`
}

type Contracts interface {
	// GetActiveContractIds returns all active contract ids (for the /status endpoint).
	GetActiveContractIds() ([]int, error)

	// NewContract creates (or reactivates) a contract from a Medsenger /init request.
	NewContract(contractID, clinicID int, locale string) error

	// UpdateContractWithPatientData stores patient metadata fetched from Medsenger.
	UpdateContractWithPatientData(contractID int, patientName, patientEmail string) error

	// MarkInactiveContractWithID soft-deletes a contract (for the /remove endpoint).
	MarkInactiveContractWithID(id int) error

	// Get returns a contract by id.
	Get(id int) (*Contract, error)
}

type contracts struct {
	db *sqlx.DB
}

func NewContracts(db *sqlx.DB) Contracts {
	return &contracts{db: db}
}

func (c *contracts) GetActiveContractIds() ([]int, error) {
	contractIds := make([]int, 0)
	err := c.db.Select(&contractIds, `SELECT id FROM contract WHERE is_active = true`)
	return contractIds, err
}

func (c *contracts) NewContract(contractID, clinicID int, locale string) error {
	const query = `
		INSERT INTO contract (id, is_active, clinic_id, locale)
		VALUES ($1, TRUE, $2, $3) ON CONFLICT (id)
		DO UPDATE SET is_active = EXCLUDED.is_active, clinic_id = EXCLUDED.clinic_id, locale = EXCLUDED.locale
	`
	_, err := c.db.Exec(query, contractID, clinicID, locale)
	return err
}

func (c *contracts) UpdateContractWithPatientData(contractID int, patientName, patientEmail string) error {
	const query = `UPDATE contract SET patient_name = $1, patient_email = $2 WHERE id = $3`
	_, err := c.db.Exec(query, patientName, patientEmail, contractID)
	return err
}

func (c *contracts) MarkInactiveContractWithID(id int) error {
	_, err := c.db.Exec(`UPDATE contract SET is_active = false WHERE id = $1`, id)
	return err
}

func (c *contracts) Get(id int) (*Contract, error) {
	var contract Contract
	err := c.db.Get(&contract, "SELECT * FROM contract WHERE id = $1", id)
	return &contract, err
}
