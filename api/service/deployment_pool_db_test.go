package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

// A deploy's transaction checks the caller's access to the secrets and resources its variables
// read on its own connection, so a deploy never waits for a second one from the pool.
func TestDeployWithSecretVariablesNeedsOneConnection(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.storeSecret(t, newTestKeyProvider(t, "k1"), databaseURLName, databaseURLValue)
	seedDeployment(t, f, sameRegionSpec)

	config := f.pool.Config()
	config.MaxConns = 1
	single, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(single.Close)
	server := NewDeploymentServer(
		single, genDb.New(single), &fakeResolver{digest: testDigest}, testRegistryHost, testServiceDefaults(),
		testClusterSilentAfter, testMaxPlacementBytes, limits.Defaults(), 8, nil,
	)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{
		ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb,
		Scopes: []genDb.EntityScope{
			{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
			{EntityType: genDb.EntityTypeOrganization, EntityID: f.tenant.orgID, Scope: genDb.ScopeRead},
		},
	})
	service := imageService()
	service.Env = secretEnvOf([]string{databaseURLName})

	_, err = server.CreateDeployment(ctx, &deploymentv1.CreateDeploymentRequest{
		ResourceId:    f.resourceID.String(),
		Regions:       []string{testRegion},
		EnvironmentId: f.envID.String(),
		Spec:          &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	})
	if err != nil {
		t.Fatalf("deploy on a one-connection pool: %v", err)
	}
}
