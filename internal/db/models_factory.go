package db

import (
	"github.com/jmoiron/sqlx"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db/models"
)

// ModelsFactory aggregates the per-entity repositories.
type ModelsFactory interface {
	Contracts() models.Contracts
	NeyroxAccounts() models.NeyroxAccounts
}

type modelsFactory struct {
	contracts      models.Contracts
	neyroxAccounts models.NeyroxAccounts
}

func newModelsFactory(db *sqlx.DB) ModelsFactory {
	return &modelsFactory{
		contracts:      models.NewContracts(db),
		neyroxAccounts: models.NewNeyroxAccounts(db),
	}
}

func (f *modelsFactory) Contracts() models.Contracts {
	return f.contracts
}

func (f *modelsFactory) NeyroxAccounts() models.NeyroxAccounts {
	return f.neyroxAccounts
}
