package main

import (
	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/bviews"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/router"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/assert"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/config"
)

func main() {
	cfg := config.LoadConfigFromEnv()

	if !cfg.Server.Debug {
		assert.NoErr(util.StartSentry(cfg.SentryDSN))
	}

	modelsFactory, err := db.Connect(cfg.DB)
	assert.NoErr(err)

	maigoClient := maigo.Init(cfg.Server.MedsengerAgentKey)
	deps := util.NewDependencies(cfg, maigoClient, modelsFactory)

	bviews.Host = cfg.Host

	r := router.New(cfg)
	router.RegisterRoutes(r, deps)
	assert.NoErr(router.Start(r, cfg))
}
