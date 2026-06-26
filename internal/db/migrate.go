package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"

	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/config"
)

// migrationsFS embeds the SQL migration files into the binary so migrations can
// be applied without shipping the source tree or the goose CLI.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

const migrationsDir = "migrations"

func init() {
	goose.SetBaseFS(migrationsFS)
}

// Migrate runs a goose migration command (e.g. "up", "down", "status", "reset")
// against the database described by cfg using the embedded migrations.
func Migrate(cfg *config.Database, command string, args ...string) error {
	sqldb, err := sql.Open("postgres", DataSourceName(cfg))
	if err != nil {
		return fmt.Errorf("open db for migration: %w", err)
	}
	defer sqldb.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.RunContext(context.Background(), command, sqldb, migrationsDir, args...); err != nil {
		return fmt.Errorf("goose %s: %w", command, err)
	}
	return nil
}
