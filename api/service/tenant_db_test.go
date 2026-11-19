package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/auth"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/tvm"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

// testMachine is the vending machine the service tests issue sessions and API tokens from.
func testMachine(queries genDb.Querier) *tvm.VendingMachine {
	return tvm.NewVendingMachine(queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
		MaxAPITokenDuration:         time.Hour,
	})
}

// openResolver signs up anyone with a verified email.
func openResolver(t *testing.T, pool *pgxpool.Pool) *auth.Resolver {
	t.Helper()
	policy, err := auth.ParseSignupPolicy("open")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	return auth.NewResolver(pool, policy, limits.Defaults(), time.Hour)
}

// signUp resolves a verified testIssuer identity, creating its user on first sign-in.
func signUp(t *testing.T, resolver *auth.Resolver, subject, email string) uuid.UUID {
	t.Helper()
	user, _, err := resolver.Resolve(t.Context(), auth.Identity{
		Issuer: testIssuer, Subject: subject, Email: email, EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("sign up %s: %v", email, err)
	}
	return user.ID
}

// tenant is a signed-up owner and their organization. Its methods add workspaces, environments
// and resources through the RPCs a client calls, with the role grants and events they record.
type tenant struct {
	pool    *pgxpool.Pool
	queries *genDb.Queries
	ownerID uuid.UUID
	orgID   uuid.UUID
}

func seedTenant(t *testing.T, pool *pgxpool.Pool, queries *genDb.Queries, email, org string) tenant {
	t.Helper()
	tn := tenant{pool: pool, queries: queries, ownerID: signUp(t, openResolver(t, pool), email, email)}
	orgs := NewOrgServer(pool, queries, InvitationConfig{}, limits.Defaults())
	created, err := orgs.CreateOrg(tn.owner(t), &orgv1.CreateOrgRequest{Name: new(org)})
	if err != nil {
		t.Fatalf("create org %s: %v", org, err)
	}
	tn.orgID = uuid.MustParse(created.GetOrgId())
	return tn
}

// owner is a request context carrying the owner's current scopes.
func (tn tenant) owner(t *testing.T) context.Context {
	t.Helper()
	return scoped(t, tn.queries, tn.ownerID)
}

// workspace creates a workspace, which comes with its default environment.
func (tn tenant) workspace(t *testing.T, name string) uuid.UUID {
	t.Helper()
	server := NewWorkspaceServer(tn.pool, tn.queries, limits.Defaults())
	created, err := server.CreateWorkspace(
		tn.owner(t),
		&workspacev1.CreateWorkspaceRequest{OrgId: tn.orgID.String(), Name: name},
	)
	if err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}
	return uuid.MustParse(created.GetWorkspaceId())
}

func (tn tenant) defaultEnvironment(t *testing.T, workspaceID uuid.UUID) uuid.UUID {
	t.Helper()
	env, err := tn.queries.GetWorkspaceDefaultEnvironment(t.Context(), workspaceID)
	if err != nil {
		t.Fatalf("default environment of %s: %v", workspaceID, err)
	}
	return env.ID
}

func (tn tenant) environment(t *testing.T, workspaceID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	server := NewEnvironmentServer(tn.pool, tn.queries, limits.Defaults())
	created, err := server.CreateEnvironment(
		tn.owner(t),
		&environmentv1.CreateEnvironmentRequest{WorkspaceId: workspaceID.String(), Name: name},
	)
	if err != nil {
		t.Fatalf("create environment %s: %v", name, err)
	}
	return uuid.MustParse(created.GetEnvironmentId())
}

func (tn tenant) renameEnvironment(t *testing.T, environmentID uuid.UUID, name string) {
	t.Helper()
	if _, err := tn.pool.Exec(
		t.Context(), `UPDATE environments SET name = $2, display_name = $2 WHERE id = $1`, environmentID, name,
	); err != nil {
		t.Fatalf("rename environment to %s: %v", name, err)
	}
}

func (tn tenant) resource(t *testing.T, workspaceID uuid.UUID, name string, spec *resourcev1.ServiceSpec) uuid.UUID {
	t.Helper()
	server := NewResourceServer(tn.pool, tn.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	created, err := server.CreateResource(tn.owner(t), &resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID.String(),
		Name:        name,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:        &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: spec}},
	})
	if err != nil {
		t.Fatalf("create resource %s: %v", name, err)
	}
	return uuid.MustParse(created.GetResourceId())
}

func environmentRevision(t *testing.T, queries genDb.Querier, environmentID uuid.UUID) int64 {
	t.Helper()
	env, err := queries.GetEnvironmentByID(t.Context(), environmentID)
	if err != nil {
		t.Fatalf("get environment %s: %v", environmentID, err)
	}
	return env.Revision
}
