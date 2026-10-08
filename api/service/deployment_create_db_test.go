package service

import (
	"context"
	"encoding/json"
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
	resourceSpec string,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
	service := &deploymentv1.ServiceDeploymentSpec{Build: build, Port: 8080}
	return createDeploymentWith(t, f, resourceSpec, service)
}

func createDeploymentWith(
	t *testing.T,
	f *deployFixture,
	resourceSpec string,
	service *deploymentv1.ServiceDeploymentSpec,
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
	server := NewDeploymentServer(f.pool, f.queries, machine, resolver, testRegistryHost, testServiceDefaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	scoped := context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	spec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}}
	req := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId:    f.resourceID.String(),
		Region:        testRegion,
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
	_, err := createDeployment(t, f, otherRegionSpec)
	wantCode(t, err, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after a rejected deploy, want 0", n)
	}
}

func TestCreateDeploymentReturnsThePinnedImage(t *testing.T) {
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	build := created.GetBuild()
	want := "index.docker.io/library/nginx@" + testDigest
	if build.GetType() != buildSourceTypeImage || build.GetImage() != want {
		t.Fatalf("build = %v, want image %s", build, want)
	}
}

const unboundedRegionSpec = `{"regions":{"us-east-1":{"enabled":true,"primary":true,"cpu":"100m","memory":"64Mi"}}}`

func imageService() *deploymentv1.ServiceDeploymentSpec {
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
	return &deploymentv1.ServiceDeploymentSpec{Build: build, Port: 8080}
}

func desiredPayload(t *testing.T, f *deployFixture) ApplicationPayload {
	t.Helper()
	var payload ApplicationPayload
	desired := f.placement(t, f.clusterID).DesiredSpec
	if err := json.Unmarshal(desired, &payload); err != nil {
		t.Fatalf("decode desired spec: %v", err)
	}
	return payload
}

func TestCreateDeploymentRaisesAnUnsetMaximumToTheMinimum(t *testing.T) {
	f := newDeployFixture(t)
	service := imageService()
	minReplicas := int32(5)
	service.MinReplicas = &minReplicas
	if _, err := createDeploymentWith(t, f, unboundedRegionSpec, service); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	payload := desiredPayload(t, f)
	replicas := payload.AppSpec.ServiceSpec.Resources.Replicas
	if replicas.Min != minReplicas || replicas.Max != minReplicas {
		t.Fatalf("replicas = %+v, want %d-%d", replicas, minReplicas, minReplicas)
	}
}

func TestCreateDeploymentRejectsSpecsTheControllerRejects(t *testing.T) {
	cases := map[string]func(*deploymentv1.ServiceDeploymentSpec){
		"explicit max below min": func(s *deploymentv1.ServiceDeploymentSpec) {
			minReplicas := int32(3)
			maxReplicas := int32(2)
			s.MinReplicas = &minReplicas
			s.MaxReplicas = &maxReplicas
		},
		"cpu below the controller minimum": func(s *deploymentv1.ServiceDeploymentSpec) {
			cpu := "50m"
			s.Cpu = &cpu
		},
		"max replicas above the controller maximum": func(s *deploymentv1.ServiceDeploymentSpec) {
			maxReplicas := int32(15)
			s.MaxReplicas = &maxReplicas
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDeployFixture(t)
			service := imageService()
			change(service)
			_, err := createDeploymentWith(t, f, unboundedRegionSpec, service)
			wantCode(t, err, connect.CodeInvalidArgument)
			if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
				t.Fatalf("%d deployments after a rejected deploy, want 0", n)
			}
		})
	}
}
