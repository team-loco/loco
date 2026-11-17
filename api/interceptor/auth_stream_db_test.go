package interceptor

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/auth"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

func TestStreamProviderValidationDoesNotRecreateADeletedUser(t *testing.T) {
	t.Parallel()
	policy, err := auth.ParseSignupPolicy("open")
	if err != nil {
		t.Fatal(err)
	}
	f := newProviderFixture(t, policy)
	token := f.issuer.Sign("k1", f.issuer.Claims("stream-user", "stream@example.test", true))
	ctx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, withBearer(token))
	if err != nil {
		t.Fatal(err)
	}
	original, _ := reqctx.PrincipalFrom(ctx)
	if err = f.queries.DeleteUser(t.Context(), original.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.interceptor.revalidate(ctx, original); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("deleted stream user = %v, want Unauthenticated", err)
	}
	var users int
	if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Fatal("stream validation provisioned an account")
	}
	login, _ := auth.LoginFrom(ctx)
	replacement, _, err := f.interceptor.resolver.Resolve(t.Context(), login.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == original.UserID {
		t.Fatal("replacement reused the deleted user ID")
	}
	if _, err = f.interceptor.revalidate(ctx, original); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("replacement user inherited original stream: %v", err)
	}
}

func TestStreamProviderValidationIsReadOnlyAndChecksStatus(t *testing.T) {
	t.Parallel()
	policy, err := auth.ParseSignupPolicy("open")
	if err != nil {
		t.Fatal(err)
	}
	f := newProviderFixture(t, policy)
	token := f.issuer.Sign("k1", f.issuer.Claims("stream-user", "stream@example.test", true))
	ctx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, withBearer(token))
	if err != nil {
		t.Fatal(err)
	}
	original, _ := reqctx.PrincipalFrom(ctx)
	old := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Microsecond)
	if _, err = f.pool.Exec(
		t.Context(),
		"UPDATE identities SET last_login_at = $1 WHERE user_id = $2",
		old,
		original.UserID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = f.interceptor.revalidate(ctx, original); err != nil {
		t.Fatal(err)
	}
	var touched time.Time
	if err = f.pool.QueryRow(t.Context(), "SELECT last_login_at FROM identities WHERE user_id = $1", original.UserID).
		Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if !touched.Equal(old) {
		t.Fatal("stream validation changed the identity login time")
	}
	if _, err = f.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
		ID: original.UserID, Status: genDb.UserStatusSuspended,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.interceptor.revalidate(ctx, original); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("suspended stream user = %v, want Unauthenticated", err)
	}
}
