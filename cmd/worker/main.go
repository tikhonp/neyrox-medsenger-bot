// Command worker periodically pulls measurements from Neyrox and pushes them to
// Medsenger. It runs an endless ticker loop; one cycle = neyroxsync.RunOnce.
package main

import (
	"log"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/neyroxsync"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/assert"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/config"
	neyroxclient "github.com/tikhonp/medsenger-neyrox-bot/internal/util/neyrox_client"
)

func main() {
	cfg := config.LoadConfigFromEnv()

	if !cfg.Server.Debug {
		assert.NoErr(util.StartSentry(cfg.SentryDSN))
		defer sentry.Flush(2 * time.Second)
	}

	modelsFactory, err := db.Connect(cfg.DB)
	assert.NoErr(err)

	maigoClient := maigo.Init(cfg.Server.MedsengerAgentKey)
	nc := neyroxclient.New(cfg.NeyroxAPIHost)
	syncer := neyroxsync.New(modelsFactory, maigoClient, nc)

	log.Printf("Worker started; polling every %s", cfg.FetchSleepDuration)
	ticker := time.NewTicker(cfg.FetchSleepDuration)
	defer ticker.Stop()
	for {
		if err := syncer.RunOnce(); err != nil {
			log.Printf("worker cycle error: %v", err)
			sentry.CaptureException(err)
		}
		<-ticker.C
	}
}
