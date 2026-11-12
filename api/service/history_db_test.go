package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/limits"
)

func (f *deployFixture) seedDeployment(
	t *testing.T,
	clusterID uuid.UUID,
	region, status string,
	active bool,
	age string,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
INSERT INTO deployments (resource_id, cluster_id, region, replicas, status, is_active, message, environment_id,
                         spec, spec_version, actor_type, actor_id, method, started_at, created_at)
VALUES ($1, $2, $3, 1, $4, $5, '', $6, '{}', 1, 'user', $1, 'cli', NOW(), NOW() - $7::interval)
RETURNING id`, f.resourceID, clusterID, region, status, active, f.envID, age).Scan(&id); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	return id
}

func (f *deployFixture) deploymentExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := f.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM deployments WHERE id = $1)`, id).
		Scan(&exists); err != nil {
		t.Fatalf("look up deployment: %v", err)
	}
	return exists
}

func TestDeploymentHistoryKeepsActiveUnfinishedAndPlacedDeployments(t *testing.T) {
	f := newDeployFixture(t)
	f.setLimit(t, limits.DeploymentHistory, 2)
	finished := f.seedDeployment(t, f.clusterID, testRegion, "succeeded", false, "5 days")
	unfinished := f.seedDeployment(t, f.clusterID, testRegion, "pending", false, "4 days")
	placed := f.seedDeployment(t, f.otherCluster, testRegion, "failed", false, "3 days")
	active := f.seedDeployment(t, f.otherCluster, "eu-west-1", "running", true, "2 days")
	if _, err := f.pool.Exec(t.Context(), `
INSERT INTO placements (resource_id, cluster_id, region, deployment_id, environment_id, desired_spec)
VALUES ($1, $2, $3, $4, $5, '{}')`, f.resourceID, f.otherCluster, testRegion, placed, f.envID); err != nil {
		t.Fatalf("seed placement: %v", err)
	}

	for range 2 {
		if _, err := f.deploy(t.Context(), staticSpec); err != nil {
			t.Fatalf("deploy: %v", err)
		}
	}

	if f.deploymentExists(t, finished) {
		t.Error("the finished deployment past the history limit survived")
	}
	for name, id := range map[string]uuid.UUID{"unfinished": unfinished, "placed": placed, "active": active} {
		if !f.deploymentExists(t, id) {
			t.Errorf("the %s deployment past the history limit was deleted", name)
		}
	}
}

func (f *buildFixture) seedBuild(t *testing.T, status string, sourceDeleted, imageDeleted bool, age string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := f.pool.Exec(t.Context(), `
INSERT INTO builds (id, resource_id, status, source_type, source_key, source_size, dockerfile_path,
                    image_repository, image_digest, created_by, created_at, finished_at,
                    source_deleted_at, image_deleted_at)
VALUES ($1, $2, $3::build_status, 'upload', $7, 1, 'Dockerfile', 'registry.test/app',
        CASE WHEN $3 = 'succeeded' THEN 'sha256:' || md5($7) END, $2,
        NOW() - $6::interval,
        CASE WHEN $3 IN ('succeeded', 'failed', 'canceled') THEN NOW() - $6::interval END,
        CASE WHEN $4 THEN NOW() END, CASE WHEN $5 THEN NOW() END)`,
		id, f.resourceID, status, sourceDeleted, imageDeleted, age, id.String()); err != nil {
		t.Fatalf("seed build: %v", err)
	}
	return id
}

func (f *buildFixture) buildExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := f.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM builds WHERE id = $1)`, id).
		Scan(&exists); err != nil {
		t.Fatalf("look up build: %v", err)
	}
	return exists
}

func TestBuildHistoryKeepsBuildsTheSweepersStillNeed(t *testing.T) {
	f := newBuildFixture(t)
	f.setLimit(t, limits.BuildHistory, 1)
	swept := f.seedBuild(t, "succeeded", true, true, "6 days")
	sweptFailure := f.seedBuild(t, "failed", true, false, "5 days")
	imageKept := f.seedBuild(t, "succeeded", true, false, "4 days")
	sourceKept := f.seedBuild(t, "failed", false, false, "3 days")
	running := f.seedBuild(t, "running", false, false, "2 days")

	f.create(t, 1)

	for name, id := range map[string]uuid.UUID{"swept": swept, "swept failure": sweptFailure} {
		if f.buildExists(t, id) {
			t.Errorf("the %s build past the history limit survived", name)
		}
	}
	for name, id := range map[string]uuid.UUID{
		"undeleted image":  imageKept,
		"undeleted source": sourceKept,
		"running":          running,
	} {
		if !f.buildExists(t, id) {
			t.Errorf("the build with an %s past the history limit was deleted", name)
		}
	}
}
