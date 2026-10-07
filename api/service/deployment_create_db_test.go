package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

const (
	otherRegionSpec = `{"regions":{"eu-west-1":{"enabled":true,"primary":true,"cpu":"100m",` +
		`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	sameRegionSpec = `{"regions":{"us-east-1":{"enabled":true,"primary":true,"cpu":"100m",` +
		`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
)

func createDeployment(
	t *testing.T,
	f *deployFixture,
	region string,
	resourceSpec string,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	ctx := context.Background()
	setup := `
WITH r AS (
    UPDATE resources SET spec = $2 WHERE id = $1 RETURNING id
), c AS (
    UPDATE clusters SET health_status = 'healthy'
)
INSERT INTO resource_domains (resource_id, domain, domain_source, is_primary)
SELECT id, 'svc.example.com', 'user_provided', true FROM r`
	if _, err := f.pool.Exec(ctx, setup, f.resourceID, resourceSpec); err != nil {
		t.Fatalf("seed: %v", err)
	}

	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	resolver := &fakeResolver{digest: testDigest}
	server := NewDeploymentServer(f.pool, f.queries, machine, resolver, testRegistryHost)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	scoped := context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
	service := &deploymentv1.ServiceDeploymentSpec{Build: build, Port: 8080}
	spec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}}
	req := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId:    f.resourceID.String(),
		Region:        region,
		EnvironmentId: f.envID.String(),
		Spec:          spec,
	})
	resp, err := server.CreateDeployment(scoped, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func TestCreateDeploymentRejectsARegionTheResourceDoesNotRunIn(t *testing.T) {
	f := newDeployFixture(t)
	_, err := createDeployment(t, f, "us-east-1", otherRegionSpec)
	wantCode(t, err, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after a rejected deploy, want 0", n)
	}
}

func TestCreateDeploymentReturnsThePinnedImage(t *testing.T) {
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, "us-east-1", sameRegionSpec)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	build := created.GetBuild()
	want := "index.docker.io/library/nginx@" + testDigest
	if build.GetType() != buildSourceTypeImage || build.GetImage() != want {
		t.Fatalf("build = %v, want image %s", build, want)
	}
}
