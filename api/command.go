package main

import (
	"context"
	"errors"

	"github.com/team-loco/loco/api/migrations"
)

var errCommandUsage = errors.New("usage: loco-api [migrate]")

const migrateCommand = "migrate"

type migrationConfig struct {
	DatabaseURL string
}

func runCommand(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != migrateCommand {
		return errCommandUsage
	}
	cfg, err := newMigrationConfig()
	if err != nil {
		return err
	}
	return migrations.Up(ctx, cfg.DatabaseURL)
}

func newMigrationConfig() (migrationConfig, error) {
	cfg := migrationConfig{DatabaseURL: stringEnv("DATABASE_URL", "")}
	if cfg.DatabaseURL == "" {
		return migrationConfig{}, errDatabaseURLMissing
	}
	return cfg, nil
}
