package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

var defaultEnvironmentLimit = int64(limits.EnvironmentsPerWorkspace.Value(limits.Defaults()))

func TestConcurrentEnvironmentCreationRespectsWorkspaceLimit(t *testing.T) {
	t.Parallel()
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead} {
		t.Run(string(isolation), func(t *testing.T) { testEnvironmentQuota(t, isolation) })
	}
}

func testEnvironmentQuota(t *testing.T, isolation pgx.TxIsoLevel) {
	t.Helper()
	f := newWebhookFixture(t)
	config := f.pool.Config()
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = string(isolation)
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	queries := genDb.New(pool)
	server := NewEnvironmentServer(pool, queries, limits.Defaults())
	workspaceID := uuid.MustParse(f.workspaceID)
	existing, err := f.queries.CountWorkspaceEnvironments(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for i := existing; i < defaultEnvironmentLimit-1; i++ {
		if _, createErr := server.CreateEnvironment(f.owner, newEnvironmentRequest(f.workspaceID)); createErr != nil {
			t.Fatal(createErr)
		}
	}
	holder, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, holder)
	var locked uuid.UUID
	if lockErr := holder.QueryRow(t.Context(), "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", workspaceID).
		Scan(&locked); lockErr != nil {
		t.Fatal(lockErr)
	}
	results := make(chan error, webhookQuotaContenders)
	for range webhookQuotaContenders {
		go func() {
			_, createErr := server.CreateEnvironment(f.owner, newEnvironmentRequest(f.workspaceID))
			results <- createErr
		}()
	}
	dbtest.WaitForLockWaiters(t, f.pool, webhookQuotaContenders)
	otherCtx, cancel := context.WithTimeout(f.owner, testLockTimeout)
	defer cancel()
	if _, otherErr := server.CreateEnvironment(
		otherCtx,
		&environmentv1.CreateEnvironmentRequest{WorkspaceId: f.otherID, Name: "staging"},
	); otherErr != nil {
		t.Fatalf("unrelated workspace blocked: %v", otherErr)
	}
	if rollbackErr := holder.Rollback(t.Context()); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	succeeded, limited := 0, 0
	for range webhookQuotaContenders {
		select {
		case createErr := <-results:
			switch {
			case createErr == nil:
				succeeded++
			case wireCode(createErr) == connect.CodeResourceExhausted:
				limited++
			default:
				t.Fatalf("create failed: %v", createErr)
			}
		case <-time.After(testLockTimeout):
			t.Fatal("environment creation did not finish after releasing the lock")
		}
	}
	if succeeded != 1 || limited != 1 {
		t.Fatalf("created=%d limited=%d, want 1 each", succeeded, limited)
	}
	count, err := f.queries.CountWorkspaceEnvironments(t.Context(), workspaceID)
	if err != nil || count != defaultEnvironmentLimit {
		t.Fatalf("environment count=%d error=%v", count, err)
	}
}

func newEnvironmentRequest(workspaceID string) *environmentv1.CreateEnvironmentRequest {
	return &environmentv1.CreateEnvironmentRequest{WorkspaceId: workspaceID, Name: "env-" + uuid.NewString()}
}
