package authtest

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/migrations"
)

func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := admin.Close(context.Background()); closeErr != nil {
			t.Logf("close admin connection: %v", closeErr)
		}
	})
	dbName := "auth_" + uuid.NewString()[:8]
	ident := pgx.Identifier{dbName}.Sanitize()
	if _, createErr := admin.Exec(ctx, "CREATE DATABASE "+ident); createErr != nil {
		t.Fatalf("create database: %v", createErr)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	parsed.Path = "/" + dbName
	if migrateErr := migrations.Up(ctx, parsed.String()); migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}
	pool, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, dropErr := admin.Exec(context.Background(), "DROP DATABASE "+ident+" WITH (FORCE)"); dropErr != nil {
			t.Logf("drop database: %v", dropErr)
		}
	})
	return pool
}
