package service

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
)

var testSecretNames = []string{databaseURL, "SESSION_KEY"}

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
