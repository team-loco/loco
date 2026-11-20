package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

func TestPersonalTokenLifecycleIsNotAudited(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	server := NewTokenServer(f.pool, f.queries, f.machine, limits.Defaults())
	user := genDb.Entity{Type: genDb.EntityTypeUser, ID: f.ownerID}
	request := tokenRequest(user, genDb.ScopeRead)
	if _, err := server.CreateToken(f.owner, request); err != nil {
		t.Fatal(err)
	}
	if _, err := server.RevokeToken(f.owner, &tokenv1.RevokeTokenRequest{
		Name: request.GetName(), EntityType: request.GetEntityType(), EntityId: request.GetEntityId(),
	}); err != nil {
		t.Fatal(err)
	}
	org := genDb.Entity{Type: genDb.EntityTypeOrganization, ID: f.orgID}
	if _, err := server.CreateToken(f.owner, tokenRequest(org, genDb.ScopeRead)); err != nil {
		t.Fatal(err)
	}
	var personal, orgOwned int
	const count = "SELECT count(*) FROM events WHERE type LIKE 'token.%' AND data->>'entityType' = $1"
	if err := f.pool.QueryRow(t.Context(), count, string(genDb.EntityTypeUser)).Scan(&personal); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), count, string(genDb.EntityTypeOrganization)).Scan(&orgOwned); err != nil {
		t.Fatal(err)
	}
	if personal != 0 || orgOwned != 1 {
		t.Fatalf("token events: personal %d, organization %d, want 0 and 1", personal, orgOwned)
	}
}

func TestPersonalTokenActionsAreAuditedInTheOrganization(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	secrets := f.server(t, newTestKeyProvider(t, "k1"))
	user := genDb.Entity{Type: genDb.EntityTypeUser, ID: f.ownerID}
	ctx := f.tokenContext(t, user, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: f.orgID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeOrganization, EntityID: f.orgID, Scope: genDb.ScopeRead},
	})
	if _, err := secrets.SetSecrets(ctx, &secretv1.SetSecretsRequest{
		EnvironmentId: f.environmentID.String(),
		Values:        map[string]string{stripeKey: stripeValue},
	}); err != nil {
		t.Fatalf("set secret with a personal token: %v", err)
	}
	principal, _ := reqctx.PrincipalFrom(ctx)
	var actorType string
	var actorIsToken, inOrg bool
	const latest = `SELECT actor_type, actor_id = $1, org_id = $2 FROM events WHERE type = 'secret.set'
ORDER BY seq DESC LIMIT 1`
	if err := f.pool.QueryRow(t.Context(), latest, principal.ActorID, f.orgID).
		Scan(&actorType, &actorIsToken, &inOrg); err != nil {
		t.Fatal(err)
	}
	if actorType != string(genDb.DeploymentActorTypeApiToken) || !actorIsToken || !inOrg {
		t.Fatalf("secret.set actor %s (token %t), in organization %t", actorType, actorIsToken, inOrg)
	}
}
