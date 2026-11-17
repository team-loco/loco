package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestConfigAdminRemovalAndAccountDeletionShareLockOrder(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	r := adminResolver(t, pool, "open", adminEmail)
	user, _, err := r.Resolve(t.Context(), identity("admin", adminEmail, true))
	if err != nil {
		t.Fatal(err)
	}
	// Pause the startup reconciler while it holds the claim row. Without its
	// account lock, deletion takes the grant row and waits for that claim,
	// while the reconciler later waits for the same grant row.
	const gateKey = 741025
	_, err = pool.Exec(t.Context(), `
CREATE FUNCTION pause_admin_removal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(741025);
    RETURN OLD;
END;
$$;
CREATE TRIGGER pause_admin_removal BEFORE DELETE ON system_admin_claims
FOR EACH ROW EXECUTE FUNCTION pause_admin_removal();`)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := gate.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback gate: %v", err)
		}
	})
	if _, err := gate.Exec(t.Context(), "SELECT pg_advisory_xact_lock($1)", gateKey); err != nil {
		t.Fatal(err)
	}
	synced := make(chan error, 1)
	go func() { synced <- SyncSystemAdmins(t.Context(), pool, nil) }()
	dbtest.WaitForLockWaiters(t, pool, 1)
	deleted := make(chan error, 1)
	go func() { deleted <- genDb.New(pool).DeleteUser(t.Context(), user.ID) }()
	dbtest.WaitForLockWaiters(t, pool, 2)
	if err := gate.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-synced; err != nil {
		t.Fatalf("configuration removal: %v", err)
	}
	if err := <-deleted; err != nil {
		t.Fatalf("account deletion: %v", err)
	}
}
