package tvm_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	queries "github.com/team-loco/loco/api/gen/db"
)

func TestValidateTokenDoesNotTouchCredentialActivity(t *testing.T) {
	t.Parallel()
	f := newPrincipalFixture(t)
	personal, personalRow := f.issue(t, queries.Entity{Type: queries.EntityTypeUser, ID: f.user})
	owned, ownedRow := f.issue(t, queries.Entity{Type: queries.EntityTypeWorkspace, ID: f.workspace})
	session, _, err := f.machine.IssueSession(t.Context(), f.user, nil, netip.Addr{}, "stream")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Microsecond)
	if _, err = f.pool.Exec(t.Context(), "UPDATE api_tokens SET last_used_at = $1", old); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "UPDATE session_tokens SET last_used_at = $1", old); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{personal, owned, session} {
		if _, err = f.machine.ValidateToken(t.Context(), token); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{personalRow.ID, ownedRow.ID} {
		var actual time.Time
		if err = f.pool.QueryRow(t.Context(), "SELECT last_used_at FROM api_tokens WHERE id = $1", id).
			Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if !actual.Equal(old) {
			t.Fatalf("stream validation touched token activity: %s", actual)
		}
	}
	var actual time.Time
	if err = f.pool.QueryRow(t.Context(), "SELECT last_used_at FROM session_tokens WHERE user_id = $1", f.user).
		Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if !actual.Equal(old) {
		t.Fatalf("stream validation touched session activity: %s", actual)
	}
}
