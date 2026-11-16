package dbtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/migrations"
)

const (
	transactionPollInterval = 10 * time.Millisecond
	lockWaitTimeout         = 5 * time.Second
	unreachableDatabaseURL  = "postgres://loco@127.0.0.1:1/loco"
	templatePrefix          = "loco_template_"
	// templateLockKey serializes template builds across test binaries sharing one server.
	templateLockKey = 7_120_334_901
)

var template = sync.OnceValues(buildTemplate)

// NewPool returns a pool on a freshly migrated database that is dropped when the test ends.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	templateName, err := template()
	if err != nil {
		t.Fatalf("migrate template database: %v", err)
	}
	dbName := "test_" + uuid.NewString()[:8]
	ident := pgx.Identifier{dbName}.Sanitize()
	adminExec(t, baseURL,
		"CREATE DATABASE "+ident+" TEMPLATE "+pgx.Identifier{templateName}.Sanitize()+" STRATEGY FILE_COPY")
	dbURL, err := databaseURL(baseURL, dbName)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		adminExec(t, baseURL, "DROP DATABASE "+ident+" WITH (FORCE)")
	})
	return pool
}

// ClosedPool returns a pool that fails every query, standing in for a database outage.
func ClosedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), unreachableDatabaseURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	pool.Close()
	return pool
}

// WaitForLockWaiters blocks until at least want sessions in the pool's database wait on a lock.
func WaitForLockWaiters(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), lockWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(transactionPollInterval)
	defer ticker.Stop()
	for {
		var waiting int
		if err := pool.QueryRow(
			ctx,
			"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'",
		).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%d of %d sessions waited on a lock after %s", waiting, want, lockWaitTimeout)
		case <-ticker.C:
		}
	}
}

// buildTemplate returns the migrated database for the embedded schema.
func buildTemplate() (string, error) {
	ctx := context.Background()
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	hash, err := migrationsHash()
	if err != nil {
		return "", err
	}
	name := templatePrefix + hash
	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer admin.Close(ctx)
	if _, err = admin.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey); err != nil {
		return "", fmt.Errorf("lock template build: %w", err)
	}
	var exists bool
	if err = admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).
		Scan(&exists); err != nil {
		return "", fmt.Errorf("look up template: %w", err)
	}
	if exists {
		return name, nil
	}
	// A crash mid-migration leaves only the build name behind, never a half-migrated template.
	building := pgx.Identifier{name + "_build"}.Sanitize()
	if _, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+building); err != nil {
		return "", fmt.Errorf("drop stale template build: %w", err)
	}
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+building); err != nil {
		return "", fmt.Errorf("create template build: %w", err)
	}
	buildURL, err := databaseURL(baseURL, name+"_build")
	if err != nil {
		return "", err
	}
	if err = migrations.Up(ctx, buildURL); err != nil {
		return "", err
	}
	if _, err = admin.Exec(ctx, "ALTER DATABASE "+building+" RENAME TO "+pgx.Identifier{name}.Sanitize()); err != nil {
		return "", fmt.Errorf("publish template: %w", err)
	}
	return name, nil
}

func migrationsHash() (string, error) {
	sum := sha256.New()
	err := fs.WalkDir(migrations.Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, readErr := fs.ReadFile(migrations.Files, path)
		if readErr != nil {
			return readErr
		}
		sum.Write([]byte(path))
		sum.Write(content)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash migrations: %w", err)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16], nil
}

func adminExec(t *testing.T, baseURL, statement string) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, execErr := admin.Exec(ctx, statement)
	if err := errors.Join(execErr, admin.Close(ctx)); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func databaseURL(baseURL, name string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}
