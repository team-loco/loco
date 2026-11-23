package service

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	testPrimaryDomain = "svc.example.com"
	otherRegionSpec   = `{"routing":{"port":8080},"regions":{"us-va-1":{"enabled":true,"cpu":"100m",` +
		`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	sameRegionSpec = `{"routing":{"port":8080},"regions":{"us-ca-1":{"enabled":true,"cpu":"100m",` +
		`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
)

func createTestDeployment(
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
		Regions:       []string{testRegion},
		EnvironmentId: environmentID.String(),
		Spec:          &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	})
}

// deployRequest deploys as a caller with write on the fixture's resource and the extra scopes.
func deployRequest(
	t *testing.T,
	f *deployFixture,
	req *deploymentv1.CreateDeploymentRequest,
	extra ...genDb.EntityScope,
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
		nil,
	)
	scopes := append([]genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}, extra...)
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
VALUES ($1, $2, $3, 'custom', $4)`
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
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
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
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	if n := f.count(t, `SELECT count(*) FROM live_deployment_placements WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d live deployments, want 1", n)
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
	_, err := createTestDeployment(t, f, otherRegionSpec)
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
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	build := created.GetBuild()
	want := "index.docker.io/library/nginx:1.27@" + testDigest
	if build.GetType() != buildSourceTypeImage || build.GetImage() != want {
		t.Fatalf("build = %v, want image %s", build, want)
	}
}

const unboundedRegionSpec = `{"routing":{"port":8080},` +
	`"regions":{"us-ca-1":{"enabled":true,"cpu":"100m","memory":"64Mi"}}}`

// testTarget is the request's sizing of testRegion, added when missing.
func testTarget(service *deploymentv1.ServiceDeploymentSpec) *deploymentv1.RegionDeploymentTarget {
	if service.GetRegions()[testRegion] == nil {
		service.Regions = map[string]*deploymentv1.RegionDeploymentTarget{testRegion: {}}
	}
	return service.GetRegions()[testRegion]
}

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
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
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
	testTarget(service).MinReplicas = &minReplicas
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
	testTarget(service).MinReplicas = new(int32(3))
	testTarget(service).MaxReplicas = new(int32(2))
	_, err := createDeploymentWith(t, f, unboundedRegionSpec, service)
	wantCode(t, err, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after a rejected deploy, want 0", n)
	}
}

func TestCreateDeploymentRejectsAHealthTimeoutAtOrAboveTheInterval(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	service := imageService()
	service.HealthCheck = &deploymentv1.HealthCheckConfig{
		Path:             "/ready",
		IntervalSeconds:  10,
		TimeoutSeconds:   10,
		FailureThreshold: 3,
	}
	_, err := createDeploymentWith(t, f, unboundedRegionSpec, service)
	wantCode(t, err, connect.CodeInvalidArgument)
	if err == nil || !strings.Contains(err.Error(), "healthCheck.timeout must be less than healthCheck.interval") {
		t.Fatalf("error = %v, want the timeout and interval named", err)
	}
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
	testTarget(service).MinReplicas = &minReplicas
	testTarget(service).MaxReplicas = &maxReplicas
	service.HealthCheck = &deploymentv1.HealthCheckConfig{
		Path:             "/healthz",
		IntervalSeconds:  10,
		TimeoutSeconds:   2,
		FailureThreshold: 3,
	}
	testTarget(service).Scalers = &deploymentv1.Scalers{CpuTarget: &cpuTarget}
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
	var regions map[string]map[string]json.RawMessage
	if err := json.Unmarshal(keys["regions"], &regions); err != nil {
		t.Fatalf("decode stored regions: %v", err)
	}
	for _, key := range []string{"minReplicas", "maxReplicas"} {
		if _, ok := regions[testRegion][key]; !ok {
			t.Errorf("stored region has no %q key, want the proto JSON name\n%s", key, stored)
		}
	}
	if _, ok := keys["healthCheck"]; !ok {
		t.Errorf("stored spec has no healthCheck key, want the proto JSON name\n%s", stored)
	}

	got := &deploymentv1.ServiceDeploymentSpec{}
	if err := protojson.Unmarshal(stored, got); err != nil {
		t.Fatalf("protojson.Unmarshal stored spec: %v", err)
	}
	if !proto.Equal(got.GetHealthCheck(), service.GetHealthCheck()) {
		t.Errorf("health check = %v, want %v", got.GetHealthCheck(), service.GetHealthCheck())
	}
	region := got.GetRegions()[testRegion]
	if !proto.Equal(region.GetScalers(), testTarget(service).GetScalers()) {
		t.Errorf("scalers = %v, want %v", region.GetScalers(), testTarget(service).GetScalers())
	}
	if region.GetMinReplicas() != minReplicas || region.GetMaxReplicas() != maxReplicas {
		t.Errorf(
			"replicas = %d-%d, want %d-%d",
			region.GetMinReplicas(),
			region.GetMaxReplicas(),
			minReplicas,
			maxReplicas,
		)
	}
	if !maps.Equal(got.GetEnv(), service.GetEnv()) {
		t.Errorf("env = %v, want %v", got.GetEnv(), service.GetEnv())
	}
}

func TestCreateDeploymentAcceptsEnabledAutoscaling(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	service := imageService()
	testTarget(service).MinReplicas = new(int32(1))
	testTarget(service).MaxReplicas = new(int32(3))
	testTarget(service).Scalers = &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(70))}
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

const workerRegionSpec = `{"regions":{"us-ca-1":{"enabled":true,"cpu":"100m",` +
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

func TestCreateDeploymentCarriesCommandAndArgs(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	for _, tc := range []struct {
		name          string
		command, args []string
	}{
		{name: "command and args", command: []string{"/http-echo"}, args: []string{"-listen=:8080", "-text=hello world"}},
		{name: "args only", args: []string{"-text=bye"}},
		{name: "image defaults"},
	} {
		f.finishRollouts(t)
		service := imageService()
		service.Command = tc.command
		service.Args = tc.args
		created, err := deployService(t, f, service)
		if err != nil {
			t.Fatalf("%s: create deployment: %v", tc.name, err)
		}
		stored, err := f.queries.GetDeploymentByID(t.Context(), uuid.MustParse(created.GetDeploymentId()))
		if err != nil {
			t.Fatalf("%s: get deployment: %v", tc.name, err)
		}
		spec, err := converter.DeserializeDeploymentSpec(stored.Spec, genDb.ResourceTypeService)
		if err != nil {
			t.Fatalf("%s: decode spec: %v", tc.name, err)
		}
		snapshot := spec.GetService()
		if !slices.Equal(snapshot.GetCommand(), tc.command) || !slices.Equal(snapshot.GetArgs(), tc.args) {
			t.Errorf("%s: snapshot command %q args %q", tc.name, snapshot.GetCommand(), snapshot.GetArgs())
		}
		deployment := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment
		if !slices.Equal(deployment.Command, tc.command) || !slices.Equal(deployment.Args, tc.args) {
			t.Errorf("%s: placement command %q args %q", tc.name, deployment.Command, deployment.Args)
		}
	}
}

func TestCreateDeploymentRejectsAnEmptyCommandEntry(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	service := imageService()
	service.Command = []string{"/bin/sh", ""}
	_, err := deployService(t, f, service)
	wantCode(t, err, connect.CodeInvalidArgument)
}
