package util

import (
	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/config"
)

// Dependencies is the explicit dependency-injection container passed to every
// app module. Handler types are defined as aliases of it.
type Dependencies struct {
	Cfg   *config.Config
	Maigo *maigo.Client
	DB    db.ModelsFactory
}

func NewDependencies(cfg *config.Config, mc *maigo.Client, database db.ModelsFactory) Dependencies {
	return Dependencies{
		Cfg:   cfg,
		Maigo: mc,
		DB:    database,
	}
}
