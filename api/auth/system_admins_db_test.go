package auth

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
)

const adminEmail = "admin@example.test"

func adminResolver(t *testing.T, pool *pgxpool.Pool, mode string, admins ...string) *Resolver {
	t.Helper()
	policy, err := ParseSignupPolicy(mode)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	parsed, err := ParseSystemAdmins(admins)
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	if err := SyncSystemAdmins(t.Context(), pool, parsed); err != nil {
		t.Fatalf("sync admins: %v", err)
	}
	return NewResolver(pool, policy, limits.Defaults(), time.Hour, WithSystemAdmins(parsed))
}

func isSystemAdmin(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) bool {
	t.Helper()
	var admin bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM user_scopes
		WHERE user_id = $1 AND scope = 'admin' AND entity_type = 'system' AND entity_id = $2)`,
		userID, uuid.Nil).Scan(&admin); err != nil {
		t.Fatalf("read system admin scope: %v", err)
	}
	return admin
}

type adminEvent struct {
	Subject uuid.UUID
	Data    map[string]string
}

func adminEvents(t *testing.T, pool *pgxpool.Pool, eventType string) []adminEvent {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT subject_id, data FROM events WHERE type = $1 ORDER BY created_at`, eventType)
	if err != nil {
		t.Fatalf("read %s events: %v", eventType, err)
	}
	defer rows.Close()
	var found []adminEvent
	for rows.Next() {
		var event adminEvent
		var raw []byte
		if err := rows.Scan(&event.Subject, &raw); err != nil {
			t.Fatalf("scan %s event: %v", eventType, err)
		}
		if err := json.Unmarshal(raw, &event.Data); err != nil {
			t.Fatalf("decode %s event: %v", eventType, err)
		}
		found = append(found, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s events: %v", eventType, err)
	}
	return found
}

func TestResolveGrantsSystemAdminToAListedVerifiedEmail(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := adminResolver(t, pool, "open", "Admin@Example.test")

	user, _, err := r.Resolve(t.Context(), identity("admin", adminEmail, true))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !isSystemAdmin(t, pool, user.ID) {
		t.Fatal("listed email did not get system admin")
	}
	claimed := adminEvents(t, pool, events.SystemAdminClaimed)
	want := map[string]string{
		events.FieldEmail: adminEmail, events.FieldIssuer: testIssuerURL, events.FieldSubject: "admin",
		events.FieldSource: events.SourceConfig,
	}
	if len(claimed) != 1 || claimed[0].Subject != user.ID || !maps.Equal(claimed[0].Data, want) {
		t.Fatalf("claimed events = %+v, want one for %s with %v", claimed, user.ID, want)
	}

	other, _, err := r.Resolve(t.Context(), identity("other", "other@example.test", true))
	if err != nil {
		t.Fatalf("unlisted resolve: %v", err)
	}
	if isSystemAdmin(t, pool, other.ID) {
		t.Fatal("unlisted email got system admin")
	}
}

func TestResolveLetsAListedEmailSignUpWhenSignupIsClosed(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := adminResolver(t, pool, "closed", adminEmail)

	user, _, err := r.Resolve(t.Context(), identity("admin", adminEmail, true))
	if err != nil {
		t.Fatalf("listed email under closed signup: %v", err)
	}
	if !isSystemAdmin(t, pool, user.ID) {
		t.Fatal("listed email did not get system admin")
	}
	if _, _, err = r.Resolve(t.Context(), identity("other", "other@example.test", true)); !isRejection(err) {
		t.Fatalf("unlisted email under closed signup err = %v, want a rejection", err)
	}
}

func TestResolveNeverGrantsSystemAdminToAnUnverifiedEmail(t *testing.T) {
	pool := dbtest.NewPool(t)
	user, _, err := adminResolver(t, pool, "open").Resolve(t.Context(), identity("admin", adminEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	r := adminResolver(t, pool, "closed", adminEmail)

	if _, _, err = r.Resolve(t.Context(), identity("admin", adminEmail, false)); err != nil {
		t.Fatalf("unverified sign-in: %v", err)
	}
	if isSystemAdmin(t, pool, user.ID) {
		t.Fatal("unverified email got system admin")
	}
	if _, _, err = r.Resolve(t.Context(), identity("squatter", "squat@example.test", false)); !isRejection(err) {
		t.Fatalf("unverified new identity err = %v, want a rejection", err)
	}

	if _, _, err = r.Resolve(t.Context(), identity("admin", adminEmail, true)); err != nil {
		t.Fatalf("verified sign-in: %v", err)
	}
	if !isSystemAdmin(t, pool, user.ID) {
		t.Fatal("existing user's verified sign-in did not claim the listed email")
	}
}

func TestResolveGrantsAClaimedEmailToNobodyElse(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := adminResolver(t, pool, "open", adminEmail)
	first, _, err := r.Resolve(t.Context(), identity("first", adminEmail, true))
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if _, _, err = r.Resolve(t.Context(), identity("first", "moved@example.test", true)); err != nil {
		t.Fatalf("email change: %v", err)
	}

	second, _, err := r.Resolve(t.Context(), identity("second", adminEmail, true))
	if err != nil {
		t.Fatalf("second identity at the claimed email: %v", err)
	}
	if second.ID == first.ID || isSystemAdmin(t, pool, second.ID) {
		t.Fatalf("second identity resolved to %s, admin %v; want a new user without system admin",
			second.ID, isSystemAdmin(t, pool, second.ID))
	}
	if !isSystemAdmin(t, pool, first.ID) {
		t.Fatal("claiming user lost system admin")
	}
}

func TestResolveNeverReopensAClaimWhenTheClaimingUserIsDeleted(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := adminResolver(t, pool, "open", adminEmail)
	first, _, err := r.Resolve(t.Context(), identity("first", adminEmail, true))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err = genDb.New(pool).DeleteUser(t.Context(), first.ID); err != nil {
		t.Fatalf("delete the claiming user: %v", err)
	}

	r = adminResolver(t, pool, "open", adminEmail)
	next, _, err := r.Resolve(t.Context(), Identity{
		Issuer: otherIssuerURL, Subject: "reassigned", Email: adminEmail, EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("new holder of the address signs in: %v", err)
	}
	if isSystemAdmin(t, pool, next.ID) {
		t.Fatal("a deleted admin's address gave system admin to its new holder")
	}
	if claimed := adminEvents(t, pool, events.SystemAdminClaimed); len(claimed) != 1 {
		t.Fatalf("%d claims, want only the first", len(claimed))
	}
}

func TestSyncSystemAdminsRevokesARemovedEmailAndReopensItWhenReadded(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := adminResolver(t, pool, "open", adminEmail)
	user, _, err := r.Resolve(t.Context(), identity("admin", adminEmail, true))
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	r = adminResolver(t, pool, "open")
	if isSystemAdmin(t, pool, user.ID) {
		t.Fatal("removed email kept system admin")
	}
	revoked := adminEvents(t, pool, events.SystemAdminRevoked)
	want := map[string]string{
		events.FieldEmail: adminEmail, events.FieldIssuer: testIssuerURL, events.FieldSubject: "admin",
		events.FieldSource: events.SourceConfig,
	}
	if len(revoked) != 1 || revoked[0].Subject != user.ID || !maps.Equal(revoked[0].Data, want) {
		t.Fatalf("revoked events = %+v, want one for %s with %v", revoked, user.ID, want)
	}
	if _, _, err = r.Resolve(t.Context(), identity("admin", adminEmail, true)); err != nil {
		t.Fatalf("sign-in after removal: %v", err)
	}
	if isSystemAdmin(t, pool, user.ID) {
		t.Fatal("sign-in after removal granted system admin")
	}

	r = adminResolver(t, pool, "open", adminEmail)
	if isSystemAdmin(t, pool, user.ID) {
		t.Fatal("re-adding the email granted system admin before a sign-in")
	}
	if _, _, err = r.Resolve(t.Context(), identity("admin", adminEmail, true)); err != nil {
		t.Fatalf("sign-in after re-adding: %v", err)
	}
	if !isSystemAdmin(t, pool, user.ID) {
		t.Fatal("sign-in after re-adding did not claim the email")
	}
}

func TestResolveConcurrentSignInsClaimOnce(t *testing.T) {
	pool := dbtest.NewPool(t)
	r := adminResolver(t, pool, "closed", adminEmail)

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, _, errs[i] = r.Resolve(context.Background(), identity("admin", adminEmail, true))
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if claimed := adminEvents(t, pool, events.SystemAdminClaimed); len(claimed) != 1 {
		t.Fatalf("%d claims, want 1", len(claimed))
	}
}

func isRejection(err error) bool {
	_, ok := errors.AsType[*SignupRejectedError](err)
	return ok
}
