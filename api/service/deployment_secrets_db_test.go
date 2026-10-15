package service

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

var testSecretNames = []string{"DATABASE_URL", "SESSION_KEY"}

func TestDeploymentRecordsSecretNamesAndEnvironmentOnThePlacement(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	params := f.paramsFor(f.clusterID)
	params.SecretNames = testSecretNames

	var id uuid.UUID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		id, deployErr = createDeploymentWithCleanup(ctx, qtx, params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	deployment, err := f.queries.GetDeploymentByID(ctx, id)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if !slices.Equal(deployment.SecretNames, testSecretNames) {
		t.Fatalf("deployment secret names = %v, want %v", deployment.SecretNames, testSecretNames)
	}
	placement := f.placement(t, f.clusterID)
	if placement.EnvironmentID != f.envID {
		t.Fatalf("placement environment = %s, want %s", placement.EnvironmentID, f.envID)
	}
	if !slices.Equal(placement.SecretNames, testSecretNames) {
		t.Fatalf("placement secret names = %v, want %v", placement.SecretNames, testSecretNames)
	}
}

func TestScaleKeepsTheSecretNamesOfTheDeployment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	setNames := `UPDATE deployments SET secret_names = $2 WHERE resource_id = $1 AND is_active`
	if _, err := f.pool.Exec(ctx, setNames, f.resourceID, testSecretNames); err != nil {
		t.Fatalf("set secret names: %v", err)
	}

	if err := scaleResource(t, f, 2); err != nil {
		t.Fatalf("scale: %v", err)
	}

	placement := f.placement(t, f.clusterID)
	if !slices.Equal(placement.SecretNames, testSecretNames) {
		t.Fatalf("placement secret names after scale = %v, want %v", placement.SecretNames, testSecretNames)
	}
	withNames := `SELECT count(*) FROM deployments
WHERE resource_id = $1 AND secret_names = '{"DATABASE_URL","SESSION_KEY"}'`
	if n := f.count(t, withNames); n != 2 {
		t.Fatalf("%d deployments carry the names, want both", n)
	}
}

func TestRedeployAndRollbackKeepTheCurrentEnvironmentValues(t *testing.T) {
	f := newDeployFixture(t)
	provider := testLocalProvider(t, "k1")
	serverConfig := testAgentConfig()
	first := imageService()
	first.Env = map[string]string{"MODE": "original", "KEEP": "retained"}
	original, err := createDeploymentWith(t, f, sameRegionSpec, first)
	if err != nil {
		t.Fatal(err)
	}
	var storedSpec []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT spec FROM deployments WHERE id = $1`, original.GetDeploymentId()).
		Scan(&storedSpec); err != nil {
		t.Fatal(err)
	}
	rollback := &deploymentv1.ServiceDeploymentSpec{}
	if err := protojson.Unmarshal(storedSpec, rollback); err != nil {
		t.Fatal(err)
	}
	setNames := `UPDATE deployments SET secret_names = $2 WHERE resource_id = $1 AND is_active`
	if _, err := f.pool.Exec(t.Context(), setNames, f.resourceID, testSecretNames); err != nil {
		t.Fatal(err)
	}
	placementNames := `UPDATE placements SET secret_names = $2 WHERE resource_id = $1`
	if _, err := f.pool.Exec(t.Context(), placementNames, f.resourceID, testSecretNames); err != nil {
		t.Fatal(err)
	}
	f.storeSecret(t, provider, "DATABASE_URL", stripeValue)
	f.storeSecret(t, provider, "SESSION_KEY", sessionKeyValue)
	latest := map[string]string{"MODE": "current", "KEEP": "retained"}
	explicit := imageService()
	explicit.Env = latest
	if _, err := createDeploymentWith(t, f, sameRegionSpec, explicit); err != nil {
		t.Fatal(err)
	}
	for _, service := range []*deploymentv1.ServiceDeploymentSpec{imageService(), rollback} {
		if _, err := createDeploymentWith(t, f, sameRegionSpec, service); err != nil {
			t.Fatal(err)
		}
		if env := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; !maps.Equal(env, latest) {
			t.Fatalf("environment = %v, want current values %v", env, latest)
		}
		if names := f.placement(t, f.clusterID).SecretNames; !slices.Equal(names, testSecretNames) {
			t.Fatalf("secret names = %v, want %v", names, testSecretNames)
		}
		server := NewAgentServer(f.pool, f.queries, nil, nil, provider, testHeartbeatInterval, serverConfig)
		sent := f.sendPending(t, server)
		if len(sent) != 1 {
			t.Fatalf("sent %d messages, want 1", len(sent))
		}
		secret := sent[0].GetApply().GetEnvSecret()
		if string(secret.GetData()["DATABASE_URL"]) != stripeValue ||
			string(secret.GetData()["SESSION_KEY"]) != sessionKeyValue {
			t.Fatal("redeploy did not deliver both secret values")
		}
	}
}

func TestRedeployReadsEnvironmentFromThePreviousCluster(t *testing.T) {
	f := newDeployFixture(t)
	original := imageService()
	original.Env = map[string]string{"MOVED_CLUSTER_ENV": "keep-across-cluster-move"}
	if _, err := createDeploymentWith(t, f, sameRegionSpec, original); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE clusters SET is_active = false WHERE id = $1`,
		f.clusterID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := createDeploymentWith(t, f, sameRegionSpec, imageService()); err != nil {
		t.Fatal(err)
	}
	old := f.placement(t, f.clusterID)
	if !old.DesiredDeleted {
		t.Fatal("previous cluster placement was not removed")
	}
	f.clusterID = f.otherCluster
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; !maps.Equal(got, original.GetEnv()) {
		t.Fatalf("replacement cluster environment = %v, want %v", got, original.GetEnv())
	}
}

func TestFirstDeploymentDoesNotInheritAnotherEnvironmentsValues(t *testing.T) {
	f := newDeployFixture(t)
	original := imageService()
	original.Env = map[string]string{"ONLY_IN_PRODUCTION": "production-only-value"}
	if _, err := createDeploymentWith(t, f, sameRegionSpec, original); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE deployments SET secret_names = $2 WHERE resource_id = $1`,
		f.resourceID,
		testSecretNames,
	); err != nil {
		t.Fatal(err)
	}
	f.envID = f.addStagingEnvironment(t)
	f.setStagingTier(t, f.otherCluster)
	f.clusterID = f.otherCluster
	if _, err := createDeploymentWith(t, f, sameRegionSpec, imageService()); err != nil {
		t.Fatal(err)
	}
	if got := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.Env; len(got) != 0 {
		t.Fatalf("staging inherited production variables: %v", got)
	}
	if got := f.placement(t, f.clusterID).SecretNames; len(got) != 0 {
		t.Fatalf("staging inherited production secret names: %v", got)
	}
}
