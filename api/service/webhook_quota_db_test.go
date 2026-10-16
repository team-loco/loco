package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
)

const webhookQuotaContenders = 2

func TestConcurrentWebhookCreationRespectsWorkspaceLimit(t *testing.T) {
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
	server := NewWebhookServer(pool, queries, false)
	request := &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID,
		Url:         receiverURL,
		EventTypes:  []string{events.ResourceScaled},
	}
	for range maxWebhooksPerWorkspace - 1 {
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
	pid := holder.Conn().PgConn().PID()
	f.waitForWebhookWaiters(t, pid, webhookQuotaContenders)
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
			case connect.CodeOf(createErr) == connect.CodeFailedPrecondition:
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
	if err != nil || count != maxWebhooksPerWorkspace {
		t.Fatalf("hook count=%d error=%v", count, err)
	}
	var recorded int
	const creationEvents = `SELECT count(*) FROM events
WHERE workspace_id=$1 AND type=$2`
	if eventErr := f.pool.QueryRow(t.Context(), creationEvents, f.workspaceID, events.WebhookCreated).
		Scan(&recorded); eventErr != nil {
		t.Fatal(eventErr)
	}
	if recorded != maxWebhooksPerWorkspace {
		t.Fatalf("recorded %d webhook creation events, want %d", recorded, maxWebhooksPerWorkspace)
	}
}

func (f *webhookFixture) waitForWebhookWaiters(t *testing.T, pid uint32, want int) {
	t.Helper()
	const waiting = `WITH RECURSIVE blocked AS (
 SELECT pid FROM pg_stat_activity
 WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid))
 UNION
 SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))
 WHERE a.datname=current_database()
) SELECT count(*) FROM blocked`
	deadline := time.Now().Add(testLockTimeout)
	for time.Now().Before(deadline) {
		var count int
		if err := f.pool.QueryRow(t.Context(), waiting, int32(pid)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == want {
			return
		}
		time.Sleep(lockWaiterPoll)
	}
	t.Fatalf("%d webhook requests did not wait on the controlled workspace lock", want)
}
