package service

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	webhookv1 "github.com/team-loco/loco/gen/go/loco/webhook/v1"
)

const webhookDeleteContenders = 2

func TestConcurrentWebhookDeletionRecordsOneEvent(t *testing.T) {
	f := newWebhookFixture(t)
	server := NewWebhookServer(f.pool, f.queries, false)
	created, err := server.CreateWebhook(f.owner, &webhookv1.CreateWebhookRequest{
		WorkspaceId: f.workspaceID,
		Url:         receiverURL,
		EventTypes:  []string{events.ResourceScaled},
	})
	if err != nil {
		t.Fatal(err)
	}
	hookID := created.GetWebhook().GetId()
	holder, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, holder)
	var locked uuid.UUID
	if lockErr := holder.QueryRow(t.Context(), "SELECT id FROM webhooks WHERE id=$1 FOR UPDATE", hookID).
		Scan(&locked); lockErr != nil {
		t.Fatal(lockErr)
	}
	request := &webhookv1.DeleteWebhookRequest{WorkspaceId: f.workspaceID, WebhookId: hookID}
	results := make(chan error, webhookDeleteContenders)
	for range webhookDeleteContenders {
		go func() { _, deleteErr := server.DeleteWebhook(f.owner, request); results <- deleteErr }()
	}
	f.waitForWebhookWaiters(t, holder.Conn().PgConn().PID(), webhookDeleteContenders)
	if rollbackErr := holder.Rollback(t.Context()); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	succeeded, missing := 0, 0
	for range webhookDeleteContenders {
		select {
		case deleteErr := <-results:
			switch {
			case deleteErr == nil:
				succeeded++
			case connect.CodeOf(deleteErr) == connect.CodeNotFound:
				missing++
			default:
				t.Fatalf("delete failed: %v", deleteErr)
			}
		case <-time.After(testLockTimeout):
			t.Fatal("webhook deletion did not finish after releasing the lock")
		}
	}
	if succeeded != 1 || missing != 1 {
		t.Fatalf("deleted=%d missing=%d, want 1 each", succeeded, missing)
	}
	if _, deleteErr := server.DeleteWebhook(f.owner, request); connect.CodeOf(deleteErr) != connect.CodeNotFound {
		t.Fatalf("repeated deletion = %v, want not_found", deleteErr)
	}
	var recorded int
	const deletionEvents = `SELECT count(*) FROM events
WHERE subject_id=$1 AND type=$2`
	if eventErr := f.pool.QueryRow(t.Context(), deletionEvents, hookID, events.WebhookDeleted).
		Scan(&recorded); eventErr != nil {
		t.Fatal(eventErr)
	}
	if recorded != 1 {
		t.Fatalf("recorded %d deletion events, want 1", recorded)
	}
}
