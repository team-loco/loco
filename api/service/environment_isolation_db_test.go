package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
)

const (
	stagingName   = "staging"
	stagingDomain = "svc-staging.example.com"
)

func (f *deployFixture) addTypedEnvironment(t *testing.T, name, environmentType string) uuid.UUID {
	t.Helper()
	insert := `
INSERT INTO environments (workspace_id, name, environment_type, created_by)
SELECT $1, $2, $3, id FROM users LIMIT 1
RETURNING id`
	var id uuid.UUID
	row := f.pool.QueryRow(context.Background(), insert, f.workspaceID(t), name, environmentType)
	if err := row.Scan(&id); err != nil {
		t.Fatalf("add environment %s: %v", name, err)
	}
	return id
}

func (f *deployFixture) addStagingEnvironment(t *testing.T) uuid.UUID {
	t.Helper()
	return f.addTypedEnvironment(t, stagingName, stagingName)
}

func (f *deployFixture) setClusterTier(t *testing.T, clusterID uuid.UUID, tier string) {
	t.Helper()
	update := `UPDATE clusters SET tier = $2 WHERE id = $1`
	if _, err := f.pool.Exec(context.Background(), update, clusterID, tier); err != nil {
		t.Fatalf("set cluster tier: %v", err)
	}
}

func (f *deployFixture) activeDeployments(t *testing.T, environmentID uuid.UUID) []genDb.Deployment {
	t.Helper()
	deployments, err := f.queries.ListActiveDeploymentsForEnvironment(context.Background(), environmentID)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	return deployments
}

func TestDeploymentsOfTwoEnvironmentsInOneRegionStayActive(t *testing.T) {
	f := newDeployFixture(t)
	stagingID := f.addStagingEnvironment(t)
	f.setClusterTier(t, f.otherCluster, stagingName)
	ctx := context.Background()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	staging := f.paramsFor(f.otherCluster)
	staging.EnvironmentID = stagingID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := createDeploymentWithCleanup(ctx, qtx, staging, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy staging: %v", err)
	}
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("redeploy production: %v", err)
	}

	if got := len(f.activeDeployments(t, f.envID)); got != 1 {
		t.Fatalf("production active deployments = %d, want 1", got)
	}
	if got := len(f.activeDeployments(t, stagingID)); got != 1 {
		t.Fatalf("staging active deployments = %d, want 1 after production redeployed", got)
	}
}

func TestDeployingAnotherEnvironmentToTheSameClusterIsRefused(t *testing.T) {
	f := newDeployFixture(t)
	qaID := f.addTypedEnvironment(t, "qa", "production")
	ctx := context.Background()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	qa := f.paramsFor(f.clusterID)
	qa.EnvironmentID = qaID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := createDeploymentWithCleanup(ctx, qtx, qa, staticSpec)
		return deployErr
	})
	if !errors.Is(err, errClusterHeldByEnvironment) {
		t.Fatalf("deploy qa = %v, want %v", err, errClusterHeldByEnvironment)
	}
	if want := "cluster c1 already runs it for environment prod"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to name the cluster and the environment", err)
	}
	if got := len(f.activeDeployments(t, f.envID)); got != 1 {
		t.Fatalf("production active deployments = %d, want 1", got)
	}
}

func TestPlanSeesOnlyTheDomainsOfItsEnvironment(t *testing.T) {
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	f.setRoutedSpec(t)
	f.addRunningOwned(t)
	stagingID := f.addStagingEnvironment(t)
	insert := `
INSERT INTO resource_domains (resource_id, environment_id, domain, domain_source, is_primary)
SELECT id, $2, $3, 'user_provided', true FROM resources WHERE name = $1`
	if _, err := f.pool.Exec(context.Background(), insert, planOwned, stagingID, stagingDomain); err != nil {
		t.Fatalf("add staging domain: %v", err)
	}

	file := planFileHeader + planFileOwnedAsLive()
	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetOperations()) != 0 || len(resp.GetErrors()) != 0 {
		t.Fatalf("production plan = %v, want nothing for a domain staging holds", resp)
	}
}

func TestPlanAcceptsOnlyRegionsWithAHealthyClusterOfTheEnvironmentType(t *testing.T) {
	cases := map[string]string{
		"only another tier":    `UPDATE clusters SET tier = 'staging', health_status = 'healthy'`,
		"no healthy cluster":   `UPDATE clusters SET health_status = 'unhealthy'`,
		"health never checked": `UPDATE clusters SET health_status = NULL`,
	}
	for name, update := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDeployFixture(t)
			if _, err := f.pool.Exec(context.Background(), update); err != nil {
				t.Fatalf("update clusters: %v", err)
			}
			resp, err := plan(t, f, planFileHeader+planFileNew, f.workspaceReadScopes(t))
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			errs := resp.GetErrors()
			if len(errs) != 1 || errs[0].GetPath() != "regions.us-east-1" ||
				errs[0].GetMessage() != configplan.ErrUnknownRegion.Error()+": us-east-1" {
				t.Fatalf("errors = %v, want the region refused", errs)
			}
		})
	}
}
