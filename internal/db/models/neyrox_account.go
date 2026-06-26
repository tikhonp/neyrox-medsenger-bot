package models

import (
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// ErrNeyroxAccountNotFound is returned when a contract has no connected Neyrox account.
var ErrNeyroxAccountNotFound = errors.New("neyrox account not found")

// NeyroxAccount holds the Neyrox credentials/tokens a patient connected to a contract.
type NeyroxAccount struct {
	ID           int            `db:"id"`
	ContractID   int            `db:"contract_id"`
	Email        string         `db:"email"`
	Password     string         `db:"password"`
	AccessToken  sql.NullString `db:"access_token"`
	RefreshToken sql.NullString `db:"refresh_token"`
	// LastSync is the date_device of the newest measurement already pushed to Medsenger.
	LastSync sql.NullTime `db:"last_sync"`
	// SyncErrMsgReady gates sending a single error message per failure transition.
	SyncErrMsgReady bool `db:"sync_err_msg_ready"`
	// SyncSuccessMsgSent gates sending the "sync configured" message exactly once.
	SyncSuccessMsgSent bool `db:"sync_success_msg_sent"`
}

type NeyroxAccounts interface {
	// Connect upserts the patient's Neyrox credentials for a contract, clearing any
	// stored tokens and resetting the message-dedup flags so login is retried.
	Connect(contractID int, email, password string) (*NeyroxAccount, error)

	// GetByContractID returns the account connected to a contract, or ErrNeyroxAccountNotFound.
	GetByContractID(contractID int) (*NeyroxAccount, error)

	// Save persists changes to an existing account.
	Save(a *NeyroxAccount) error

	// DeleteByContractID removes the account connected to a contract.
	DeleteByContractID(contractID int) error

	// GetActiveToSync returns every account whose contract is active.
	GetActiveToSync() ([]NeyroxAccount, error)
}

type neyroxAccounts struct {
	db *sqlx.DB
}

func NewNeyroxAccounts(db *sqlx.DB) NeyroxAccounts {
	return &neyroxAccounts{db: db}
}

func (n *neyroxAccounts) Connect(contractID int, email, password string) (*NeyroxAccount, error) {
	const query = `
		INSERT INTO neyrox_account (contract_id, email, password)
		VALUES ($1, $2, $3) ON CONFLICT (contract_id)
		DO UPDATE SET email = EXCLUDED.email, password = EXCLUDED.password,
			access_token = NULL, refresh_token = NULL, last_sync = NULL,
			sync_err_msg_ready = TRUE, sync_success_msg_sent = FALSE
		RETURNING *
	`
	var a NeyroxAccount
	if err := n.db.Get(&a, query, contractID, email, password); err != nil {
		return nil, err
	}
	return &a, nil
}

func (n *neyroxAccounts) GetByContractID(contractID int) (*NeyroxAccount, error) {
	var a NeyroxAccount
	err := n.db.Get(&a, `SELECT * FROM neyrox_account WHERE contract_id = $1`, contractID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNeyroxAccountNotFound
	}
	return &a, err
}

func (n *neyroxAccounts) Save(a *NeyroxAccount) error {
	const query = `
		UPDATE neyrox_account SET
			email = :email, password = :password,
			access_token = :access_token, refresh_token = :refresh_token,
			last_sync = :last_sync,
			sync_err_msg_ready = :sync_err_msg_ready, sync_success_msg_sent = :sync_success_msg_sent
		WHERE id = :id
	`
	_, err := n.db.NamedExec(query, a)
	return err
}

func (n *neyroxAccounts) DeleteByContractID(contractID int) error {
	_, err := n.db.Exec(`DELETE FROM neyrox_account WHERE contract_id = $1`, contractID)
	return err
}

func (n *neyroxAccounts) GetActiveToSync() ([]NeyroxAccount, error) {
	const query = `
		SELECT a.* FROM neyrox_account a
		JOIN contract c ON c.id = a.contract_id
		WHERE c.is_active
	`
	accounts := []NeyroxAccount{}
	err := n.db.Select(&accounts, query)
	return accounts, err
}
