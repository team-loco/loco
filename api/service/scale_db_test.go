package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func scaleResource(t *testing.T, f *deployFixture, replicas int32) error {
	t.Helper()
	return scaleResourceIn(t, f, f.envID, replicas)
}

func scaleResourceIn(t *testing.T, f *deployFixture, environmentID uuid.UUID, replicas int32) error {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&resourcev1.ScaleResourceRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: environmentID.String(),
		Replicas:      &replicas,
	})
	_, err := server.ScaleResource(ctx, req)
	return err
}

func TestScaleResourceRejectsReplicasTheControllerRejects(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	err := scaleResource(t, f, 15)
	wantCode(t, err, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d deployments after a rejected scale, want 1", n)
	}
	replicas := desiredPayload(t, f).AppSpec.ServiceSpec.Resources.Replicas
	if replicas.Min != 1 || replicas.Max != 1 {
		t.Fatalf("desired replicas = %+v, want the original 1-1", replicas)
	}
}

func TestScaleResourceWritesTheReplicasIntoTheDesiredSpec(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	const scaled = 4
	if err := scaleResource(t, f, scaled); err != nil {
		t.Fatalf("scale: %v", err)
	}
	replicas := desiredPayload(t, f).AppSpec.ServiceSpec.Resources.Replicas
	if replicas.Min != scaled || replicas.Max != scaled {
		t.Fatalf("desired replicas = %+v, want %d-%d", replicas, scaled, scaled)
	}
}

func TestScaleResourceKeepsTheRouteOfTheDeployment(t *testing.T) {
	cases := map[string]struct {
		domain   string
		wantHost string
	}{
		"with a domain":    {domain: testPrimaryDomain, wantHost: testPrimaryDomain},
		"without a domain": {},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDeployFixture(t)
			if tc.domain != "" {
				f.addDomain(t, tc.domain, true)
			}
			if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
				t.Fatalf("create deployment: %v", err)
			}
			if err := scaleResource(t, f, 2); err != nil {
				t.Fatalf("scale: %v", err)
			}

			routing := desiredPayload(t, f).AppSpec.ServiceSpec.Routing
			host := ""
			if routing != nil {
				host = routing.HostName
			}
			if host != tc.wantHost {
				t.Fatalf("routed hostname = %q, want %q", host, tc.wantHost)
			}
		})
	}
}

func TestScaleResourceKeepsTheWorkspaceOrganization(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	const scaled = 2
	if err := scaleResource(t, f, scaled); err != nil {
		t.Fatalf("scale: %v", err)
	}
	want := f.organizationID(t)
	if got := desiredPayload(t, f).AppSpec.OrganizationID; got != want {
		t.Errorf("organization = %q after a scale, want the workspace's %s", got, want)
	}
}

func (f *deployFixture) deployToProductionAndStaging(t *testing.T) uuid.UUID {
	t.Helper()
	stagingID := f.addStagingEnvironment(t)
	f.setStagingTier(t, f.otherCluster)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	if _, err := deployServiceIn(t, f, imageService(), stagingID); err != nil {
		t.Fatalf("deploy staging: %v", err)
	}
	return stagingID
}

func (f *deployFixture) activeDeployment(t *testing.T, environmentID uuid.UUID) genDb.Deployment {
	t.Helper()
	deployments := f.activeDeployments(t, environmentID)
	if len(deployments) != 1 {
		t.Fatalf("environment %s has %d active deployments, want 1", environmentID, len(deployments))
	}
	return deployments[0]
}

func TestScaleResourceScalesOnlyTheRequestedEnvironment(t *testing.T) {
	for _, scaledName := range []string{statusProduction, stagingName} {
		t.Run(scaledName, func(t *testing.T) {
			f := newDeployFixture(t)
			stagingID := f.deployToProductionAndStaging(t)
			scaledID, otherID := f.envID, stagingID
			if scaledName == stagingName {
				scaledID, otherID = stagingID, f.envID
			}
			untouched := f.activeDeployment(t, otherID)

			const scaled = 3
			if err := scaleResourceIn(t, f, scaledID, scaled); err != nil {
				t.Fatalf("scale: %v", err)
			}
			if got := f.activeDeployment(t, scaledID).Replicas; got != scaled {
				t.Errorf("%s replicas = %d after the scale, want %d", scaledName, got, scaled)
			}
			other := f.activeDeployment(t, otherID)
			if other.ID != untouched.ID || other.Replicas != untouched.Replicas {
				t.Errorf("the other environment's deployment changed from %s (%d replicas) to %s (%d replicas)",
					untouched.ID, untouched.Replicas, other.ID, other.Replicas)
			}
			var recorded string
			latest := `SELECT data->>$2 FROM events WHERE type = $1 ORDER BY seq DESC LIMIT 1`
			row := f.pool.QueryRow(context.Background(), latest, events.ResourceScaled, events.FieldEnvironmentID)
			if err := row.Scan(&recorded); err != nil {
				t.Fatalf("read the scaled event: %v", err)
			}
			if recorded != scaledID.String() {
				t.Errorf("scaled event names environment %q, want %s", recorded, scaledID)
			}
		})
	}
}

func TestScaleResourceRefusesAnEnvironmentWithoutADeployment(t *testing.T) {
	f := newDeployFixture(t)
	stagingID := f.addStagingEnvironment(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	production := f.activeDeployment(t, f.envID)

	err := scaleResourceIn(t, f, stagingID, 2)
	wantCode(t, err, connect.CodeNotFound)
	if got := f.activeDeployment(t, f.envID); got.ID != production.ID {
		t.Fatalf("production deployment = %s after scaling staging, want %s", got.ID, production.ID)
	}
}

func TestScaleResourceRefusesAnEnvironmentOfAnotherWorkspace(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	insert := `
WITH w AS (
    INSERT INTO workspaces (org_id, name, created_by)
    SELECT org_id, 'other', created_by FROM workspaces LIMIT 1 RETURNING id, created_by
)
INSERT INTO environments (workspace_id, name, created_by) SELECT id, 'prod', created_by FROM w RETURNING id`
	var foreignID uuid.UUID
	if err := f.pool.QueryRow(context.Background(), insert).Scan(&foreignID); err != nil {
		t.Fatalf("add foreign environment: %v", err)
	}

	err := scaleResourceIn(t, f, foreignID, 2)
	wantCode(t, err, connect.CodeNotFound)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d deployments after a refused scale, want 1", n)
	}
}
