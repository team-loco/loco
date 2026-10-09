package service

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	testPrimaryDomain = "svc.example.com"
	otherRegionSpec   = `{"regions":{"eu-west-1":{"enabled":true,"primary":true,"cpu":"100m",` +
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
WITH c AS (
    UPDATE clusters SET health_status = 'healthy'
)
UPDATE resources SET spec = $2 WHERE id = $1`
	if _, err := f.pool.Exec(ctx, setup, f.resourceID, resourceSpec); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resolver := &fakeResolver{digest: testDigest}
	server := NewDeploymentServer(f.pool, f.queries, resolver, testRegistryHost, testServiceDefaults())
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

func (f *deployFixture) addDomain(t *testing.T, domain string, primary bool) {
	t.Helper()
	insert := `
INSERT INTO resource_domains (resource_id, domain, domain_source, is_primary)
VALUES ($1, $2, 'user_provided', $3)`
	if _, err := f.pool.Exec(context.Background(), insert, f.resourceID, domain, primary); err != nil {
		t.Fatalf("add domain %s: %v", domain, err)
	}
}

func TestCreateDeploymentRoutesThePrimaryDomain(t *testing.T) {
	f := newDeployFixture(t)
	f.addDomain(t, "alt.example.com", false)
	f.addDomain(t, testPrimaryDomain, true)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	routing := desiredPayload(t, f).AppSpec.ServiceSpec.Routing
	if routing == nil {
		t.Fatal("routing is nil, want a route for the primary domain")
	}
	if routing.HostName != testPrimaryDomain {
		t.Errorf("hostname = %q, want the primary %s", routing.HostName, testPrimaryDomain)
	}
	if routing.PathPrefix != "/" || routing.IdleTimeout != testIdleTimeout {
		t.Errorf("routing = %+v, want the configured path prefix and idle timeout", routing)
	}
}

func TestCreateDeploymentWithoutADomainHasNoRoute(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1 AND is_active`); n != 1 {
		t.Fatalf("%d active deployments, want 1", n)
	}
	service := desiredPayload(t, f).AppSpec.ServiceSpec
	if service.Routing != nil {
		t.Fatalf("routing = %+v, want none for a resource without a domain", service.Routing)
	}
	if service.Deployment.Port != 8080 {
		t.Errorf("port = %d, want 8080", service.Deployment.Port)
	}
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

func TestCreateDeploymentStoresTheSpecAsProtoJSON(t *testing.T) {
	f := newDeployFixture(t)
	service := imageService()
	minReplicas := int32(1)
	maxReplicas := int32(2)
	cpuTarget := int32(70)
	service.MinReplicas = &minReplicas
	service.MaxReplicas = &maxReplicas
	service.HealthCheck = &deploymentv1.HealthCheckConfig{
		Path:             "/healthz",
		IntervalSeconds:  10,
		TimeoutSeconds:   2,
		FailureThreshold: 3,
	}
	service.Scalers = &deploymentv1.Scalers{Enabled: true, CpuTarget: &cpuTarget}
	service.Env = map[string]string{"SECRET": "plaintext"}
	if _, err := createDeploymentWith(t, f, unboundedRegionSpec, service); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	var stored []byte
	query := `SELECT spec FROM deployments WHERE resource_id = $1`
	if err := f.pool.QueryRow(context.Background(), query, f.resourceID).Scan(&stored); err != nil {
		t.Fatalf("read stored spec: %v", err)
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(stored, &keys); err != nil {
		t.Fatalf("decode stored spec: %v", err)
	}
	for _, key := range []string{"healthCheck", "minReplicas", "maxReplicas"} {
		if _, ok := keys[key]; !ok {
			t.Errorf("stored spec has no %q key, want the proto JSON name\n%s", key, stored)
		}
	}
	if _, ok := keys["env"]; ok {
		t.Errorf("stored spec carries env\n%s", stored)
	}

	got := &deploymentv1.ServiceDeploymentSpec{}
	if err := protojson.Unmarshal(stored, got); err != nil {
		t.Fatalf("protojson.Unmarshal stored spec: %v", err)
	}
	if !proto.Equal(got.GetHealthCheck(), service.GetHealthCheck()) {
		t.Errorf("health check = %v, want %v", got.GetHealthCheck(), service.GetHealthCheck())
	}
	if !proto.Equal(got.GetScalers(), service.GetScalers()) {
		t.Errorf("scalers = %v, want %v", got.GetScalers(), service.GetScalers())
	}
	if got.GetMinReplicas() != minReplicas || got.GetMaxReplicas() != maxReplicas {
		t.Errorf("replicas = %d-%d, want %d-%d", got.GetMinReplicas(), got.GetMaxReplicas(), minReplicas, maxReplicas)
	}
	if len(got.GetEnv()) != 0 {
		t.Errorf("env = %v, want none stored", got.GetEnv())
	}
}
