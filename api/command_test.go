package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCommandRejectsUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"unknown"}, {migrateCommand, "extra"}} {
		if err := runCommand(t.Context(), args); !errors.Is(err, errCommandUsage) {
			t.Fatalf("runCommand(%q) = %v, want usage error", args, err)
		}
	}
}

func TestMigrateRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if err := runCommand(t.Context(), []string{migrateCommand}); !errors.Is(err, errDatabaseURLMissing) {
		t.Fatalf("migrate = %v, want missing database URL", err)
	}
}

func TestMigrateReturnsDatabaseFailure(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://127.0.0.1:1/missing?sslmode=disable&connect_timeout=1")
	if err := runCommand(t.Context(), []string{migrateCommand}); err == nil {
		t.Fatal("migration against an unreachable database succeeded")
	}
}

func TestMigrateAppliesAndReplaysWithoutServerConfiguration(t *testing.T) {
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	admin, err := pgx.Connect(t.Context(), baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := admin.Close(context.Background()); closeErr != nil {
			t.Error(closeErr)
		}
	})
	name := "migration_command_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{name}.Sanitize()
	if _, createErr := admin.Exec(t.Context(), "CREATE DATABASE "+identifier); createErr != nil {
		t.Fatal(createErr)
	}
	t.Cleanup(func() {
		if _, dropErr := admin.Exec(context.Background(), "DROP DATABASE "+identifier+" WITH (FORCE)"); dropErr != nil {
			t.Error(dropErr)
		}
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	t.Setenv("DATABASE_URL", parsed.String())
	// These would reject normal API startup, but are unrelated to migrations.
	t.Setenv("AUTH_ISSUERS", "invalid")
	t.Setenv("DATABASE_MAX_CONNS", "invalid")
	for range 2 {
		if migrateErr := runCommand(t.Context(), []string{migrateCommand}); migrateErr != nil {
			t.Fatal(migrateErr)
		}
	}
	conn, err := pgx.Connect(t.Context(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(t.Context())
	var exists bool
	if err := conn.QueryRow(t.Context(), "SELECT to_regclass('public.organizations') IS NOT NULL").
		Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("migration did not create the API schema")
	}
}
