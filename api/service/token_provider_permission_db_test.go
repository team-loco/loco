package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/tvm"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
)

func TestCheckPermissionForAuthenticatedProviderToken(t *testing.T) {
	f := newDeployFixture(t)
	issuer := authtest.NewIssuer(t)
	verifier := auth.NewVerifier(http.DefaultClient, []auth.IssuerConfig{{
		Issuer: issuer.URL(), JWKSURL: issuer.URL() + "/keys", Audience: "authenticated",
		Claims: auth.ClaimPaths{Subject: "sub", Email: "email", EmailVerified: "email_verified"},
	}})
	policy := auth.SignupPolicy{Mode: auth.SignupOpen}
	resolver := auth.NewResolver(f.pool, policy)
	identity := auth.Identity{Issuer: issuer.URL(), Subject: "reader", Email: "test@example.com", EmailVerified: true}
	user, _, err := resolver.Resolve(t.Context(), identity)
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}
	var workspaceID uuid.UUID
	if workspaceErr := f.pool.QueryRow(t.Context(), "SELECT workspace_id FROM resources WHERE id = $1", f.resourceID).
		Scan(&workspaceID); workspaceErr != nil {
		t.Fatalf("workspace: %v", workspaceErr)
	}
	grant := genDb.AddUserScopeParams{
		UserID:     user.ID,
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   workspaceID,
		Scope:      genDb.ScopeRead,
	}
	if grantErr := f.queries.AddUserScope(t.Context(), grant); grantErr != nil {
		t.Fatalf("grant read: %v", grantErr)
	}
	machine := tvm.NewVendingMachine(
		f.pool,
		f.queries,
		tvm.Config{LastUsedUpdateInterval: time.Minute, MaxAPITokenDuration: time.Hour},
	)
	t.Cleanup(machine.Close)
	authorizer := authz.New(f.pool, f.queries)
	authInterceptor := interceptor.NewAuthInterceptor(machine, verifier, resolver, authorizer)
	service := NewTokenServer(f.pool, f.queries, machine)
	serverInterceptor := authInterceptor.Interceptor()
	rpcServer := connect.NewServer(serverInterceptor)
	tokenv1connect.RegisterTokenServiceHandler(rpcServer, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	transport := connecthttp.NewTransport(server.Client(), server.URL)
	connectClient := connect.NewClient(transport)
	client := tokenv1connect.NewTokenServiceClient(connectClient)
	claims := issuer.Claims("reader", "test@example.com", true)
	token := issuer.Sign("k1", claims)
	check := func(bearer string, id uuid.UUID, scope tokenv1.Scope) (bool, error) {
		t.Helper()
		message := &tokenv1.CheckPermissionRequest{
			EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
			EntityId:   id.String(),
			Scope:      scope,
		}
		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set("Authorization", "Bearer "+bearer)
		response, callErr := client.CheckPermission(ctx, message)
		if callErr != nil {
			return false, callErr
		}
		return response.GetAllowed(), nil
	}
	for _, test := range []struct {
		name    string
		id      uuid.UUID
		scope   tokenv1.Scope
		allowed bool
	}{
		{name: "workspace read", id: workspaceID, scope: tokenv1.Scope_SCOPE_READ, allowed: true},
		{name: "workspace write", id: workspaceID, scope: tokenv1.Scope_SCOPE_WRITE},
		{name: "other workspace", id: uuid.New(), scope: tokenv1.Scope_SCOPE_READ},
	} {
		t.Run(test.name, func(t *testing.T) {
			allowed, callErr := check(token, test.id, test.scope)
			if callErr != nil || allowed != test.allowed {
				t.Fatalf("permission = %t, %v; want %t", allowed, callErr, test.allowed)
			}
		})
	}
	entity := genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: workspaceID}
	scopes := []genDb.EntityScope{{EntityType: entity.Type, EntityID: entity.ID, Scope: genDb.ScopeRead}}
	userID := user.ID.String()
	apiToken, err := machine.Issue(t.Context(), "permission-reader", userID, entity, scopes, time.Hour)
	if err != nil {
		t.Fatalf("issue API token: %v", err)
	}
	if allowed, err := check(apiToken, workspaceID, tokenv1.Scope_SCOPE_READ); err != nil || !allowed {
		t.Fatalf("API token self-check = %t, %v; want true", allowed, err)
	}
	foreignClaims := issuer.Claims("another-reader", "other@example.test", true)
	foreign := issuer.Sign("k1", foreignClaims)
	if allowed, err := check(foreign, workspaceID, tokenv1.Scope_SCOPE_READ); err != nil || allowed {
		t.Fatalf("another subject = %t, %v; want false", allowed, err)
	}
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	expired := issuer.Sign("k1", claims)
	if _, err := check(
		expired,
		workspaceID,
		tokenv1.Scope_SCOPE_READ,
	); connect.CodeOf(
		err,
	) != connect.CodeUnauthenticated {
		t.Fatalf("expired token: %v", err)
	}
	remove := genDb.RemoveUserScopeParams{
		UserID:     user.ID,
		EntityType: grant.EntityType,
		EntityID:   workspaceID,
		Scope:      grant.Scope,
	}
	if err := f.queries.RemoveUserScope(t.Context(), remove); err != nil {
		t.Fatalf("remove read: %v", err)
	}
	if allowed, err := check(token, workspaceID, tokenv1.Scope_SCOPE_READ); err != nil || allowed {
		t.Fatalf("removed scope = %t, %v; want false", allowed, err)
	}
}
