package interceptor

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

const providerOrgName = "acme"

type providerFixture struct {
	issuer      *authtest.Issuer
	interceptor *authInterceptor
	machine     *tvm.VendingMachine
	queries     *genDb.Queries
}

func newProviderFixture(t *testing.T, policy auth.SignupPolicy) *providerFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	issuer := authtest.NewIssuer(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		MaxAPITokenDuration:         time.Hour,
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	verifier := auth.NewVerifier(http.DefaultClient, []auth.IssuerConfig{{
		Issuer:   issuer.URL(),
		JWKSURL:  issuer.URL() + "/.well-known/jwks.json",
		Audience: "authenticated",
		Claims: auth.ClaimPaths{
			Subject:       "sub",
			Email:         "email",
			EmailVerified: "email_verified",
		},
	}})
	return &providerFixture{
		issuer:      issuer,
		interceptor: NewAuthInterceptor(machine, verifier, auth.NewResolver(pool, policy), authz.New(pool, queries)),
		machine:     machine,
		queries:     queries,
	}
}

func bearer(token string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	return h
}

func TestAuthenticateProviderTokenProvisionsUser(t *testing.T) {
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := newProviderFixture(t, policy)
	token := f.issuer.Sign("k1", f.issuer.Claims("sub-1", "dev@example.test", true))

	ctx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(token))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok || entity.Type != genDb.EntityTypeUser {
		t.Fatalf("entity = %+v", entity)
	}
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok || len(scopes) != 3 {
		t.Fatalf("scopes = %+v", scopes)
	}
	identity, ok := ctx.Value(contextkeys.IdentityKey).(auth.Identity)
	if !ok || identity.Subject != "sub-1" {
		t.Fatalf("identity = %+v", identity)
	}

	again, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(token))
	if err != nil {
		t.Fatalf("second authenticate: %v", err)
	}
	againEntity, ok := again.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok || againEntity.ID != entity.ID {
		t.Fatalf("second request resolved to %+v, want %s", againEntity, entity.ID)
	}
}

func TestAuthenticateProviderTokenErrors(t *testing.T) {
	policy, err := auth.ParseSignupPolicy("domains", "acme.test")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := newProviderFixture(t, policy)
	other := authtest.NewIssuer(t)

	cases := []struct {
		name  string
		token string
		want  connect.Code
	}{
		{
			"rejected by signup policy",
			f.issuer.Sign("k1", f.issuer.Claims("s", "dev@evil.test", true)),
			connect.CodePermissionDenied,
		},
		{"untrusted issuer", other.Sign("k1", other.Claims("s", "dev@acme.test", true)), connect.CodeUnauthenticated},
		{"unknown loco token", "loco_s_doesnotexist", connect.CodeUnauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(tc.token))
			if code := connect.CodeOf(err); code != tc.want {
				t.Fatalf("code = %v (%v), want %v", code, err, tc.want)
			}
		})
	}
}

func TestAuthenticateRejectsSuspendedUsers(t *testing.T) {
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := newProviderFixture(t, policy)
	providerToken := f.issuer.Sign("k1", f.issuer.Claims("sub-1", "dev@example.test", true))
	ctx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(providerToken))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	user, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		t.Fatal("no entity in context")
	}
	org, err := f.queries.CreateOrg(t.Context(), genDb.CreateOrgParams{Name: providerOrgName, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	orgScope := genDb.EntityScope{EntityType: genDb.EntityTypeOrganization, EntityID: org.ID, Scope: genDb.ScopeRead}
	if scopeErr := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: user.ID, EntityType: orgScope.EntityType, EntityID: orgScope.EntityID, Scope: orgScope.Scope,
	}); scopeErr != nil {
		t.Fatalf("grant org scope: %v", scopeErr)
	}
	sessionToken, _, err := f.machine.IssueSession(t.Context(), user.ID, nil, "", "")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	apiToken, err := f.machine.Issue(t.Context(), "ci", user.ID.String(),
		genDb.Entity{Type: genDb.EntityTypeOrganization, ID: org.ID}, []genDb.EntityScope{orgScope}, time.Hour)
	if err != nil {
		t.Fatalf("issue api token: %v", err)
	}
	if _, sessionErr := f.interceptor.authenticate(
		t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(sessionToken),
	); sessionErr != nil {
		t.Fatalf("session token before suspension: %v", sessionErr)
	}

	if _, suspendErr := f.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
		ID: user.ID, Status: genDb.UserStatusSuspended,
	}); suspendErr != nil {
		t.Fatalf("suspend: %v", suspendErr)
	}

	for name, token := range map[string]string{"provider token": providerToken, "session token": sessionToken} {
		_, authErr := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(token))
		if code := connect.CodeOf(authErr); code != connect.CodeUnauthenticated {
			t.Errorf("%s: code = %v (%v), want %v", name, code, authErr, connect.CodeUnauthenticated)
		}
	}
	orgCtx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(apiToken))
	if err != nil {
		t.Fatalf("org api token after its creator was suspended: %v", err)
	}
	if entity, ok := orgCtx.Value(contextkeys.EntityKey).(genDb.Entity); !ok || entity.ID != org.ID {
		t.Fatalf("api token entity = %+v, want org %s", entity, org.ID)
	}
}

func claimedOrgs(ctx context.Context) []auth.ClaimedOrg {
	claimed, ok := ctx.Value(contextkeys.ClaimedOrgsKey).([]auth.ClaimedOrg)
	if !ok {
		return nil
	}
	return claimed
}

func TestAuthenticatePutsClaimedOrgsInContext(t *testing.T) {
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := newProviderFixture(t, policy)
	token := f.issuer.Sign("k1", f.issuer.Claims("sub-1", "dev@example.test", true))
	ctx, err := f.interceptor.authenticate(t.Context(), userv1connect.UserServiceWhoAmIProcedure, bearer(token))
	if err != nil {
		t.Fatalf("first authenticate: %v", err)
	}
	user, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		t.Fatal("no entity in context")
	}
	if claimed := claimedOrgs(ctx); len(claimed) != 0 {
		t.Fatalf("claimed orgs before any invitation = %v", claimed)
	}
	org, err := f.queries.CreateOrg(t.Context(), genDb.CreateOrgParams{Name: providerOrgName, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, inviteErr := f.queries.CreateInvitation(t.Context(), genDb.CreateInvitationParams{
		OrgID: org.ID, Email: "dev@example.test", InvitedBy: user.ID, ExpiresAt: time.Now().Add(time.Hour),
	}); inviteErr != nil {
		t.Fatalf("invite: %v", inviteErr)
	}

	ctx, err = f.interceptor.authenticate(t.Context(), userv1connect.UserServiceWhoAmIProcedure, bearer(token))
	if err != nil {
		t.Fatalf("second authenticate: %v", err)
	}
	claimed := claimedOrgs(ctx)
	if len(claimed) != 1 || claimed[0].OrgID != org.ID || claimed[0].OrgName != providerOrgName {
		t.Fatalf("claimed orgs = %+v, want acme", claimed)
	}
	ctx, err = f.interceptor.authenticate(t.Context(), userv1connect.UserServiceWhoAmIProcedure, bearer(token))
	if err != nil {
		t.Fatalf("third authenticate: %v", err)
	}
	if again := claimedOrgs(ctx); len(again) != 0 {
		t.Fatalf("claimed orgs repeated on the next request: %+v", again)
	}
}
