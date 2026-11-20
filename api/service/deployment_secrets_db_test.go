package service

import (
	"context"
	"maps"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/varref"
)

func TestCreateDeploymentRejectsUnavailableOrOversizedSecrets(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	provider := newTestKeyProvider(t, "k1")
	f.storeSecret(t, provider, databaseURLName, stripeValue)
	f.envID = f.addStagingEnvironment(t)

	f.storeSecret(t, provider, sessionKeyName, strings.Repeat("x", testMaxPlacementBytes))
	seedDeployment(t, f, sameRegionSpec)

	for _, name := range []string{databaseURLName, sessionKeyName} {
		t.Run(name, func(t *testing.T) {
			before := f.rolloutSnapshot(t)
			service := imageService()
			service.Env = secretEnvOf([]string{name})
			_, err := deployService(t, f, service)
			wantCode(t, err, connect.CodeFailedPrecondition)
			if f.rolloutSnapshot(t) != before {
				t.Fatal("rejected deployment changed persisted state")
			}
		})
	}
}

func TestDeploymentRecordsResolvedVariablesAndEnvironmentOnThePlacement(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := context.Background()
	params := f.paramsFor(f.clusterID)
	plain := map[string]string{buildModeKey: releaseMode}
	secretEnv := secretEnvOf([]string{databaseURLName, sessionKeyName})
	params.Resolved = varref.Resolved{Plain: plain, Secret: secretEnv}

	var id uuid.UUID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		id, deployErr = recordTestDeployment(ctx, qtx, limits.Install(limits.Defaults()), params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	deployment, err := f.queries.GetDeploymentByID(ctx, id)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if !maps.Equal(deployment.PlainEnv, plain) || !maps.Equal(deployment.SecretEnv, secretEnv) {
		t.Fatalf(
			"deployment env = %v and %v, want %v and %v",
			deployment.PlainEnv,
			deployment.SecretEnv,
			plain,
			secretEnv,
		)
	}
	placement := f.placement(t, f.clusterID)
	if placement.EnvironmentID != f.envID {
		t.Fatalf("placement environment = %s, want %s", placement.EnvironmentID, f.envID)
	}
	if !maps.Equal(placement.SecretEnv, secretEnv) {
		t.Fatalf("placement secret env = %v, want %v", placement.SecretEnv, secretEnv)
	}
}

func TestScaleKeepsTheResolvedVariablesOfTheDeployment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := context.Background()
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	plain := map[string]string{apiURLName: "http://api:8080"}
	secretEnv := secretEnvOf([]string{databaseURLName})
	setEnv := `UPDATE deployments SET plain_env = $2, secret_env = $3
WHERE id IN (SELECT deployment_id FROM live_deployment_placements WHERE resource_id = $1)`
	if _, err := f.pool.Exec(ctx, setEnv, f.resourceID, plain, secretEnv); err != nil {
		t.Fatalf("set resolved variables: %v", err)
	}

	f.finishRollouts(t)
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatalf("scale: %v", err)
	}

	if got := f.placement(t, f.clusterID).SecretEnv; !maps.Equal(got, secretEnv) {
		t.Fatalf("placement secret env after scale = %v, want %v", got, secretEnv)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; !maps.Equal(got, plain) {
		t.Fatalf("application env after scale = %v, want %v", got, plain)
	}
	withEnv := `SELECT count(*) FROM deployments
WHERE resource_id = $1 AND secret_env = '{"DATABASE_URL":"${{ secrets.DATABASE_URL }}"}'::jsonb`
	if n := f.count(t, withEnv); n != 2 {
		t.Fatalf("%d deployments carry the secret env, want both", n)
	}
}

func TestFirstDeploymentDoesNotInheritAnotherEnvironmentsValues(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	original := imageService()
	original.Env = map[string]string{"ONLY_IN_PRODUCTION": "production-only-value"}
	if _, err := createDeploymentWith(t, f, sameRegionSpec, original); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE deployments SET secret_env = $2 WHERE resource_id = $1`,
		f.resourceID,
		secretEnvOf([]string{databaseURLName}),
	); err != nil {
		t.Fatal(err)
	}
	f.envID = f.addStagingEnvironment(t)

	if _, err := createDeploymentWith(t, f, sameRegionSpec, imageService()); err != nil {
		t.Fatal(err)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; len(got) != 0 {
		t.Fatalf("staging inherited production variables: %v", got)
	}
	if got := f.placement(t, f.clusterID).SecretEnv; len(got) != 0 {
		t.Fatalf("staging inherited production secret env: %v", got)
	}
}
