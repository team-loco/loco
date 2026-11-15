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
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
)

const webhookQuotaContenders = 2

var defaultWebhookLimit = int64(limits.WebhooksPerWorkspace.Value(limits.Defaults()))

func TestConcurrentWebhookCreationRespectsWorkspaceLimit(t *testing.T) {
	t.Parallel()
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead} {
		t.Run(string(isolation), func(t *testing.T) { testWebhookQuota(t, isolation) })
	}
}

func testWebhookQuota(t *testing.T, isolation pgx.TxIsoLevel) {
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
	server := NewWebhookServer(pool, queries, newTestKeyProvider(t, "k1"), false, limits.Defaults())
	request := &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID,
		Url:         receiverURL,
		EventTypes:  []string{events.ResourceScaled},
	}
	for range defaultWebhookLimit - 1 {
		if _, createErr := server.CreateWebhook(f.owner, request); createErr != nil {
			t.Fatal(createErr)
		}
	}
	holder, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, holder)
	var locked uuid.UUID
	if lockErr := holder.QueryRow(t.Context(), "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", f.workspaceID).
		Scan(&locked); lockErr != nil {
		t.Fatal(lockErr)
	}
	results := make(chan error, webhookQuotaContenders)
	for range webhookQuotaContenders {
		go func() { _, createErr := server.CreateWebhook(f.owner, request); results <- createErr }()
	}
	dbtest.WaitForLockWaiters(t, f.pool, webhookQuotaContenders)
	otherCtx, cancel := context.WithTimeout(f.owner, testLockTimeout)
	defer cancel()
	if _, otherErr := server.CreateWebhook(
		otherCtx,
		&webhookv1.CreateWebhookRequest{
			WorkspaceId: f.otherID,
			Url:         receiverURL,
			EventTypes:  []string{events.ResourceScaled},
		},
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
			t.Fatal("webhook creation did not finish after releasing the lock")
		}
	}
	if succeeded != 1 || limited != 1 {
		t.Fatalf("created=%d limited=%d, want 1 each", succeeded, limited)
	}
	count, err := f.queries.CountWorkspaceWebhooks(t.Context(), uuid.MustParse(f.workspaceID))
	if err != nil || count != defaultWebhookLimit {
		t.Fatalf("hook count=%d error=%v", count, err)
	}
	var recorded int64
	const creationEvents = `SELECT count(*) FROM events
WHERE workspace_id=$1 AND type=$2`
	if eventErr := f.pool.QueryRow(t.Context(), creationEvents, f.workspaceID, events.WebhookCreated).
		Scan(&recorded); eventErr != nil {
		t.Fatal(eventErr)
	}
	if recorded != defaultWebhookLimit {
		t.Fatalf("recorded %d webhook creation events, want %d", recorded, defaultWebhookLimit)
	}
}
