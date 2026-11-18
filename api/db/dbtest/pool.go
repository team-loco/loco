package dbtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
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
	testDatabasePrefix      = "test_"
	// templateLockKey serializes template builds across test binaries sharing one server.
	templateLockKey = 7_120_334_901
)

var (
	template = sync.OnceValues(buildTemplate)
	run      = sync.OnceValues(startRun)
)

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
	runID, err := run()
	if err != nil {
		t.Fatalf("start test run: %v", err)
	}
	dbName := fmt.Sprintf("%s%d_%s", testDatabasePrefix, runID, uuid.NewString()[:8])
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

// startRun holds an advisory lock on a random run ID for the life of the test binary, which names
// its databases, and drops the databases of earlier runs whose binary died before its cleanup ran.
func startRun() (int64, error) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("LOCO_TEST_DATABASE_URL"))
	if err != nil {
		return 0, fmt.Errorf("connect: %w", err)
	}
	id := rand.Int64() //nolint:gosec // names test databases; not a secret
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", id); err != nil {
		return 0, fmt.Errorf("lock run: %w", err)
	}
	if err = reapDeadRuns(ctx, conn); err != nil {
		return 0, err
	}
	return id, nil
}

// reapDeadRuns drops every test database whose run holds no lock: the server releases a session's
// advisory locks when the session ends, however its process died.
func reapDeadRuns(ctx context.Context, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx,
		"SELECT datname, split_part(datname, '_', 2)::bigint FROM pg_database WHERE datname ~ $1",
		"^"+testDatabasePrefix+"[0-9]+_")
	if err != nil {
		return fmt.Errorf("list test databases: %w", err)
	}
	databases, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
		Name string
		Run  int64
	}])
	if err != nil {
		return fmt.Errorf("list test databases: %w", err)
	}
	for _, database := range databases {
		var dead bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.Run).Scan(&dead); err != nil {
			return fmt.Errorf("check run %d: %w", database.Run, err)
		}
		if !dead {
			continue
		}
		_, dropErr := conn.Exec(
			ctx,
			"DROP DATABASE IF EXISTS "+pgx.Identifier{database.Name}.Sanitize()+" WITH (FORCE)",
		)
		_, unlockErr := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.Run)
		if err := errors.Join(dropErr, unlockErr); err != nil {
			return fmt.Errorf("drop %s of a dead run: %w", database.Name, err)
		}
	}
	return nil
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
