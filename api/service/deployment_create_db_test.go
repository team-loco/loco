package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	testPrimaryDomain = "svc.example.com"
	otherRegionSpec   = `{"routing":{"port":8080},"regions":{"eu-west-1":{"enabled":true,"cpu":"100m",` +
		`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	sameRegionSpec = `{"routing":{"port":8080},"regions":{"us-east-1":{"enabled":true,"cpu":"100m",` +
		`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
)

func createDeployment(
	t *testing.T,
	f *deployFixture,
	resourceSpec string,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
	service := &deploymentv1.ServiceDeploymentSpec{Build: build, Port: new(int32(8080))}
	return createDeploymentWith(t, f, resourceSpec, service)
}

func createDeploymentWith(
	t *testing.T,
	f *deployFixture,
	resourceSpec string,
	service *deploymentv1.ServiceDeploymentSpec,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	seedDeployment(t, f, resourceSpec)
	return deployService(t, f, service)
}

func seedDeployment(t *testing.T, f *deployFixture, resourceSpec string) {
	t.Helper()
	f.markClustersHealthy(t)
	setup := `UPDATE resources SET spec = $2 WHERE id = $1`
	if _, err := f.pool.Exec(context.Background(), setup, f.resourceID, resourceSpec); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func deployService(
	t *testing.T,
	f *deployFixture,
	service *deploymentv1.ServiceDeploymentSpec,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	return deployServiceIn(t, f, service, f.envID)
}

func deployServiceIn(
	t *testing.T,
	f *deployFixture,
	service *deploymentv1.ServiceDeploymentSpec,
	environmentID uuid.UUID,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	return deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
		ResourceId:    f.resourceID.String(),
		Region:        testRegion,
		EnvironmentId: environmentID.String(),
		Spec:          &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	})
}

func deployRequest(
	t *testing.T,
	f *deployFixture,
	req *deploymentv1.CreateDeploymentRequest,
) (*deploymentv1.CreateDeploymentResponse, error) {
	t.Helper()
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		&fakeResolver{digest: testDigest},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
		limits.Defaults(),
		8,
	)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb, Scopes: scopes},
	)
	return server.CreateDeployment(ctx, req)
}

func (f *deployFixture) addDomain(t *testing.T, domain string, primary bool) {
	t.Helper()
	insert := `
INSERT INTO resource_domains (resource_id, environment_id, domain, domain_source, is_primary)
VALUES ($1, $2, $3, 'user_provided', $4)`
	if _, err := f.pool.Exec(context.Background(), insert, f.resourceID, f.envID, domain, primary); err != nil {
		t.Fatalf("add domain %s: %v", domain, err)
	}
}

func (f *deployFixture) serveDomains(t *testing.T, domains ...string) {
	t.Helper()
	update := `UPDATE clusters SET health_status = 'healthy', last_heartbeat = NOW(), domains = $1`
	if _, err := f.pool.Exec(context.Background(), update, domains); err != nil {
		t.Fatalf("serve domains: %v", err)
	}
}

func TestCreateDeploymentRoutesThePrimaryDomain(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	if *service.Deployment.Port != 8080 {
		t.Errorf("port = %d, want 8080", *service.Deployment.Port)
	}
}

func TestCreateDeploymentRejectsARegionTheResourceDoesNotRunIn(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	_, err := createDeployment(t, f, otherRegionSpec)
	wantCode(t, err, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after a rejected deploy, want 0", n)
	}
}

func TestCreateDeploymentRefusesAClusterThatStoppedHeartbeating(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	silent := time.Now().Add(-2 * testClusterSilentAfter)
	if _, err := f.pool.Exec(context.Background(), `UPDATE clusters SET last_heartbeat = $1`, silent); err != nil {
		t.Fatalf("silence clusters: %v", err)
	}
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
	_, err := deployService(t, f, &deploymentv1.ServiceDeploymentSpec{Build: build, Port: new(int32(8080))})
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), errNoActiveCluster.Error()) {
		t.Fatalf("error = %v, want %v", err, errNoActiveCluster)
	}
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments on a silent cluster, want 0", n)
	}
}

func TestCreateDeploymentReturnsThePinnedImage(t *testing.T) {
	t.Parallel()
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

const unboundedRegionSpec = `{"routing":{"port":8080},` +
	`"regions":{"us-east-1":{"enabled":true,"cpu":"100m","memory":"64Mi"}}}`

func imageService() *deploymentv1.ServiceDeploymentSpec {
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
	return &deploymentv1.ServiceDeploymentSpec{Build: build, Port: new(int32(8080))}
}

func desiredPayload(t *testing.T, f *deployFixture) locoControllerV1.PlacementPayload {
	t.Helper()
	var payload locoControllerV1.PlacementPayload
	desired := f.placement(t, f.clusterID).DesiredSpec
	if err := json.Unmarshal(desired, &payload); err != nil {
		t.Fatalf("decode desired spec: %v", err)
	}
	return payload
}

func (f *deployFixture) organizationID(t *testing.T) string {
	t.Helper()
	var orgID uuid.UUID
	query := `SELECT w.org_id FROM resources r JOIN workspaces w ON w.id = r.workspace_id WHERE r.id = $1`
	if err := f.pool.QueryRow(context.Background(), query, f.resourceID).Scan(&orgID); err != nil {
		t.Fatalf("read organization: %v", err)
	}
	return orgID.String()
}

func TestCreateDeploymentCarriesTheWorkspaceOrganization(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	want := f.organizationID(t)
	if got := desiredPayload(t, f).AppSpec.OrganizationID; got != want {
		t.Errorf("organization = %q, want the workspace's %s", got, want)
	}
}

func TestCreateDeploymentRaisesAnUnsetMaximumToTheMinimum(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	f := newDeployFixture(t)
	service := imageService()
	service.MinReplicas = new(int32(3))
	service.MaxReplicas = new(int32(2))
	_, err := createDeploymentWith(t, f, unboundedRegionSpec, service)
	wantCode(t, err, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after a rejected deploy, want 0", n)
	}
}

func TestCreateDeploymentStoresTheSpecAsProtoJSON(t *testing.T) {
	t.Parallel()
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
	service.Scalers = &deploymentv1.Scalers{CpuTarget: &cpuTarget}
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

func TestCreateDeploymentAcceptsEnabledAutoscaling(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	service := imageService()
	service.MinReplicas = new(int32(1))
	service.MaxReplicas = new(int32(3))
	service.Scalers = &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(70))}
	_, err := createDeploymentWith(t, f, sameRegionSpec, service)
	if err != nil {
		t.Fatal(err)
	}
	resources := desiredPayload(t, f).AppSpec.ServiceSpec.Resources
	if !resources.Scalers.Enabled || resources.Scalers.CPUTarget != 70 || resources.Replicas.Min != 1 ||
		resources.Replicas.Max != 3 {
		t.Fatalf("CRD resources = %+v", resources)
	}
}

const workerRegionSpec = `{"regions":{"us-east-1":{"enabled":true,"cpu":"100m",` +
	`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`

func TestCreateDeploymentWithoutAPortRunsAWorker(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}
	worker := &deploymentv1.ServiceDeploymentSpec{Build: build}
	if _, err := createDeploymentWith(t, f, workerRegionSpec, worker); err != nil {
		t.Fatalf("create worker deployment: %v", err)
	}
	service := desiredPayload(t, f).AppSpec.ServiceSpec
	if service.Deployment.Port != nil || service.Routing != nil {
		t.Fatalf("worker port = %v, routing = %+v, want neither", service.Deployment.Port, service.Routing)
	}

	checked := &deploymentv1.ServiceDeploymentSpec{Build: build, HealthCheck: workerHealthCheck()}
	f.finishRollouts(t)
	_, err := createDeploymentWith(t, f, workerRegionSpec, checked)
	wantCode(t, err, connect.CodeInvalidArgument)
}
