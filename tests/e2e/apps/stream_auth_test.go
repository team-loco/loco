package apps

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestIdleLogTailEndsAfterTokenRevocation(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	token := w.MintToken(t, "log-reader", tokenv1.Scope_SCOPE_READ)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	stream, err := p.Clients(token).Observability.TailLogs(ctx, &observabilityv1.TailLogsRequest{
		WorkspaceId: w.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// An empty workspace sends a heartbeat, proving the stream opened before revocation.
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	if _, err = p.User.Token.RevokeToken(t.Context(), &tokenv1.RevokeTokenRequest{
		Name: "log-reader", EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, EntityId: w.ID,
	}); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = stream.Receive(); err != nil {
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("tail after token revocation = %v, want Unauthenticated", err)
			}
			return
		}
	}
}
