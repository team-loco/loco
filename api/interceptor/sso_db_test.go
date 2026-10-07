package interceptor

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

const (
	samlConnection = "11111111-2222-3333-4444-555555555555"
	otherSAML      = "99999999-2222-3333-4444-555555555555"
)

type ssoFixture struct {
	*providerFixture
	gatedOrg  uuid.UUID
	openOrg   uuid.UUID
	workspace uuid.UUID
	resource  uuid.UUID
}

func newSSOFixture(t *testing.T) *ssoFixture {
	t.Helper()
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := &ssoFixture{providerFixture: newProviderFixture(t, policy)}
	ctx := t.Context()
	creator, err := f.queries.CreateUser(ctx, genDb.CreateUserParams{Email: "creator@corp.test"})
	if err != nil {
		t.Fatalf("creator: %v", err)
	}
	gated, err := f.queries.CreateOrganization(
		ctx,
		genDb.CreateOrganizationParams{Name: "gated", CreatedBy: creator.ID},
	)
	if err != nil {
		t.Fatalf("gated org: %v", err)
	}
	open, err := f.queries.CreateOrganization(ctx, genDb.CreateOrganizationParams{Name: "open", CreatedBy: creator.ID})
	if err != nil {
		t.Fatalf("open org: %v", err)
	}
	f.gatedOrg, f.openOrg = gated.ID, open.ID
	if scanErr := f.pool.QueryRow(ctx,
		"INSERT INTO workspaces (org_id, name, created_by) VALUES ($1, 'default', $2) RETURNING id",
		gated.ID, creator.ID).Scan(&f.workspace); scanErr != nil {
		t.Fatalf("workspace: %v", scanErr)
	}
	if scanErr := f.pool.QueryRow(ctx,
		`INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
		VALUES ($1, 'web', 'service', '', 'healthy', '{}', 1) RETURNING id`,
		f.workspace).Scan(&f.resource); scanErr != nil {
		t.Fatalf("resource: %v", scanErr)
	}
	if _, createErr := f.queries.CreateOrgSSO(ctx, genDb.CreateOrgSSOParams{
		OrgID: gated.ID, ConnectionID: samlConnection, Issuer: f.issuer.URL(),
	}); createErr != nil {
		t.Fatalf("org sso: %v", createErr)
	}
	f.require(t, true)
	return f
}

func (f *ssoFixture) require(t *testing.T, on bool) {
	t.Helper()
	if _, err := f.queries.SetOrgRequireSSO(t.Context(), genDb.SetOrgRequireSSOParams{
		OrgID: f.gatedOrg, RequireSso: on,
	}); err != nil {
		t.Fatalf("require sso: %v", err)
	}
}

func (f *ssoFixture) grant(t *testing.T, userID uuid.UUID, entityType genDb.EntityType, entityID uuid.UUID) {
	t.Helper()
	if err := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: userID, EntityType: entityType, EntityID: entityID, Scope: genDb.ScopeWrite,
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
}

func (f *ssoFixture) signIn(t *testing.T, sub string) uuid.UUID {
	t.Helper()
	token := f.issuer.Sign("k1", f.issuer.Claims(sub, sub+"@corp.test", true))
	ctx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(token))
	if err != nil {
		t.Fatalf("authenticate %s: %v", sub, err)
	}
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		t.Fatal("no entity")
	}
	return entity.ID
}

func samlAMR(connection string) []any {
	return []any{map[string]any{"method": "sso/saml", "provider": connection, "timestamp": time.Now().Unix()}}
}

func entitiesIn(ctx context.Context, t *testing.T) map[uuid.UUID]bool {
	t.Helper()
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		t.Fatal("no scopes in context")
	}
	out := map[uuid.UUID]bool{}
	for _, s := range scopes {
		out[s.EntityID] = true
	}
	return out
}

func (f *ssoFixture) authenticate(t *testing.T, token string) context.Context {
	t.Helper()
	ctx, err := f.interceptor.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer(token))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	return ctx
}

func TestRequireSSOFiltersProviderTokens(t *testing.T) {
	f := newSSOFixture(t)
	userID := f.signIn(t, "dev")
	f.grant(t, userID, genDb.EntityTypeOrganization, f.gatedOrg)
	f.grant(t, userID, genDb.EntityTypeWorkspace, f.workspace)
	f.grant(t, userID, genDb.EntityTypeResource, f.resource)
	f.grant(t, userID, genDb.EntityTypeOrganization, f.openOrg)

	cases := []struct {
		name      string
		amr       []any
		wantGated bool
	}{
		{"password login", []any{map[string]any{"method": "password"}}, false},
		{"no amr", nil, false},
		{"another org's connection", samlAMR(otherSAML), false},
		{"this org's connection", samlAMR(samlConnection), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := f.issuer.Claims("dev", "dev@corp.test", true)
			if tc.amr != nil {
				claims["amr"] = tc.amr
			}
			got := entitiesIn(f.authenticate(t, f.issuer.Sign("k1", claims)), t)
			for _, id := range []uuid.UUID{f.gatedOrg, f.workspace, f.resource} {
				if got[id] != tc.wantGated {
					t.Fatalf("scope on %s present = %v, want %v", id, got[id], tc.wantGated)
				}
			}
			if !got[f.openOrg] || !got[userID] {
				t.Fatalf("scopes outside the gated org were dropped: %v", got)
			}
		})
	}

	f.require(t, false)
	if got := entitiesIn(
		f.authenticate(t, f.issuer.Sign("k1", f.issuer.Claims("dev", "dev@corp.test", true))),
		t,
	); !got[f.gatedOrg] {
		t.Fatal("scopes dropped while SSO is not required")
	}
}

func TestRequireSSOFiltersLocoTokens(t *testing.T) {
	f := newSSOFixture(t)
	userID := f.signIn(t, "cli")
	f.grant(t, userID, genDb.EntityTypeOrganization, f.gatedOrg)
	connection := samlConnection
	other := otherSAML

	plainSession, _, err := f.machine.IssueSession(t.Context(), userID, nil, nil, "", "test")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	ssoSession, _, err := f.machine.IssueSession(t.Context(), userID, nil, &connection, "", "test")
	if err != nil {
		t.Fatalf("sso session: %v", err)
	}
	orgScope := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: f.gatedOrg, Scope: genDb.ScopeWrite},
	}
	user := genDb.Entity{Type: genDb.EntityTypeUser, ID: userID}
	plainKey, err := f.machine.Issue(t.Context(), "plain", userID.String(), user, orgScope, time.Hour, nil)
	if err != nil {
		t.Fatalf("api token: %v", err)
	}
	ssoKey, err := f.machine.Issue(t.Context(), "sso", userID.String(), user, orgScope, time.Hour, &connection)
	if err != nil {
		t.Fatalf("sso api token: %v", err)
	}
	otherKey, err := f.machine.Issue(t.Context(), "other", userID.String(), user, orgScope, time.Hour, &other)
	if err != nil {
		t.Fatalf("other api token: %v", err)
	}

	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"session without sso", plainSession, false},
		{"session minted through sso", ssoSession, true},
		{"api token without sso", plainKey, false},
		{"api token minted through sso", ssoKey, true},
		{"api token from another connection", otherKey, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := f.authenticate(t, tc.token)
			if got := entitiesIn(ctx, t)[f.gatedOrg]; got != tc.want {
				t.Fatalf("gated org scope present = %v, want %v", got, tc.want)
			}
			connectionInCtx, ok := ctx.Value(contextkeys.SSOConnectionKey).(string)
			if tc.want && (!ok || connectionInCtx != samlConnection) {
				t.Fatalf("sso connection in context = %q", connectionInCtx)
			}
		})
	}
}

func TestRequireSSOKeepsSystemScopes(t *testing.T) {
	f := newSSOFixture(t)
	userID := f.signIn(t, "ops")
	f.grant(t, userID, genDb.EntityTypeOrganization, f.gatedOrg)
	if err := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: userID, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
	}); err != nil {
		t.Fatalf("system scope: %v", err)
	}
	got := entitiesIn(f.authenticate(t, f.issuer.Sign("k1", f.issuer.Claims("ops", "ops@corp.test", true))), t)
	if !got[uuid.Nil] || got[f.gatedOrg] {
		t.Fatalf("scopes = %v", got)
	}
}
