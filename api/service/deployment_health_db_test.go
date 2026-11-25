package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/pkg/converter"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	checkedRegionSpec = `{"routing":{"port":8080},"healthCheck":{"path":"/stored","initialDelaySeconds":7,` +
		`"intervalSeconds":10,"timeoutSeconds":2,"failureThreshold":3},"regions":{"us-ca-1":{"enabled":true,` +
		`"cpu":"100m","memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	resourceUpdatedEvents = `SELECT count(*) FROM events WHERE type = 'resource.updated' AND subject_id = $1`
	resourceRevision      = `SELECT revision FROM resources WHERE id = $1`
)

func storedHealthCheck() *deploymentv1.HealthCheckConfig {
	return &deploymentv1.HealthCheckConfig{
		Path:                "/stored",
		InitialDelaySeconds: 7,
		IntervalSeconds:     10,
		TimeoutSeconds:      2,
		FailureThreshold:    3,
	}
}

func TestCreateDeploymentHealthCheck(t *testing.T) {
	t.Parallel()
	requested := &deploymentv1.HealthCheckConfig{
		Path:             "/new",
		IntervalSeconds:  20,
		TimeoutSeconds:   4,
		FailureThreshold: 2,
	}
	for name, tc := range map[string]struct {
		health       *deploymentv1.HealthCheckConfig
		remove       bool
		wantDeployed *deploymentv1.HealthCheckConfig
		wantResource *deploymentv1.HealthCheckConfig
	}{
		"omitted inherits the resource's": {wantDeployed: storedHealthCheck(), wantResource: storedHealthCheck()},
		"set runs the requested one":      {health: requested, wantDeployed: requested, wantResource: storedHealthCheck()},
		"removed runs none and forgets":   {remove: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			seedDeployment(t, f, checkedRegionSpec)
			service := imageService()
			service.HealthCheck = tc.health
			if _, err := deployRequest(t, f, healthRequest(f, f.envID, service, tc.remove)); err != nil {
				t.Fatalf("create deployment: %v", err)
			}
			f.wantDeployedHealth(t, f.envID, tc.wantDeployed)
			if got := f.resourceHealthCheck(t); !proto.Equal(got, tc.wantResource) {
				t.Fatalf("resource health check = %v, want %v", got, tc.wantResource)
			}

			f.finishRollouts(t)
			if _, err := deployService(t, f, imageService()); err != nil {
				t.Fatalf("deploy again: %v", err)
			}
			f.wantDeployedHealth(t, f.envID, tc.wantResource)
		})
	}
}

func TestCreateDeploymentRemovingAHealthCheckUpdatesEveryEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, checkedRegionSpec)
	staging := f.addStagingEnvironment(t)
	if _, err := deployServiceIn(t, f, imageService(), staging); err != nil {
		t.Fatalf("deploy to staging: %v", err)
	}
	stagingBefore := f.revision(t, staging)
	productionBefore := f.revision(t, f.envID)

	if _, err := deployRequest(t, f, healthRequest(f, f.envID, imageService(), true)); err != nil {
		t.Fatalf("remove health check: %v", err)
	}
	stagingAfter := f.revision(t, staging)
	productionAfter := f.revision(t, f.envID)
	if stagingAfter <= stagingBefore || productionAfter <= productionBefore {
		t.Fatalf("revisions staging %d -> %d, production %d -> %d, want both advanced",
			stagingBefore, stagingAfter, productionBefore, productionAfter)
	}
	if n := f.count(t, resourceUpdatedEvents); n != 1 {
		t.Fatalf("%d resource.updated events, want 1", n)
	}
	f.wantDeployedHealth(t, staging, storedHealthCheck())
}

func TestCreateDeploymentRemovingAnAbsentHealthCheckLeavesTheResource(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	before := f.count(t, resourceRevision)
	if _, err := deployRequest(t, f, healthRequest(f, f.envID, imageService(), true)); err != nil {
		t.Fatalf("remove health check: %v", err)
	}
	if after := f.count(t, resourceRevision); after != before {
		t.Fatalf("resource revision %d -> %d, want unchanged", before, after)
	}
	if n := f.count(t, resourceUpdatedEvents); n != 0 {
		t.Fatalf("%d resource.updated events, want 0", n)
	}
	f.wantDeployedHealth(t, f.envID, nil)
}

func TestCreateDeploymentRejectedRemovalKeepsTheHealthCheck(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, checkedRegionSpec)
	staging := f.addStagingEnvironment(t)
	stagingBefore := f.revision(t, staging)
	resourceBefore := f.count(t, resourceRevision)
	service := imageService()
	testTarget(service).MinReplicas = new(int32(3))
	testTarget(service).MaxReplicas = new(int32(2))
	_, err := deployRequest(t, f, healthRequest(f, f.envID, service, true))
	wantCode(t, err, connect.CodeInvalidArgument)
	if got := f.resourceHealthCheck(t); !proto.Equal(got, storedHealthCheck()) {
		t.Fatalf("resource health check = %v, want %v", got, storedHealthCheck())
	}
	if f.revision(t, staging) != stagingBefore {
		t.Fatal("a rejected deployment advanced staging's revision")
	}
	if f.count(t, resourceRevision) != resourceBefore {
		t.Fatal("a rejected deployment advanced the resource's revision")
	}
}

func TestCreateDeploymentRejectsRemovingAHealthCheckItSets(t *testing.T) {
	t.Parallel()
	service := imageService()
	service.HealthCheck = workerHealthCheck()
	for name, tc := range map[string]struct {
		message proto.Message
		valid   bool
	}{
		"remove without a check": {removeRequest(imageService()), true},
		"remove with a check":    {removeRequest(service), false},
	} {
		t.Run(name, func(t *testing.T) {
			err := protovalidate.Validate(tc.message)
			if (err == nil) != tc.valid {
				t.Fatalf("validate = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func healthRequest(
	f *deployFixture,
	environmentID uuid.UUID,
	service *deploymentv1.ServiceDeploymentSpec,
	remove bool,
) *deploymentv1.CreateDeploymentRequest {
	return &deploymentv1.CreateDeploymentRequest{
		ResourceId:        f.resourceID.String(),
		Regions:           []string{testRegion},
		EnvironmentId:     environmentID.String(),
		Spec:              &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
		RemoveHealthCheck: remove,
	}
}

func removeRequest(service *deploymentv1.ServiceDeploymentSpec) *deploymentv1.CreateDeploymentRequest {
	return &deploymentv1.CreateDeploymentRequest{
		ResourceId:        "01a1dbb5-4966-72e2-a92b-a2560006942a",
		EnvironmentId:     "01a1dbb5-4966-72e2-a92b-a2560006942b",
		Spec:              &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
		RemoveHealthCheck: true,
	}
}

// wantDeployedHealth checks the environment's newest deployment's health check, as stored and as
// its placement renders it.
func (f *deployFixture) wantDeployedHealth(
	t *testing.T,
	environmentID uuid.UUID,
	want *deploymentv1.HealthCheckConfig,
) {
	t.Helper()
	var stored []byte
	query := `SELECT spec FROM deployments WHERE environment_id = $1 ORDER BY created_at DESC LIMIT 1`
	if err := f.pool.QueryRow(t.Context(), query, environmentID).Scan(&stored); err != nil {
		t.Fatalf("read deployment spec: %v", err)
	}
	spec := &deploymentv1.ServiceDeploymentSpec{}
	if err := protojson.Unmarshal(stored, spec); err != nil {
		t.Fatalf("decode deployment spec: %v", err)
	}
	if !proto.Equal(spec.GetHealthCheck(), want) {
		t.Fatalf("stored health check = %v, want %v", spec.GetHealthCheck(), want)
	}
	var payload locoControllerV1.PlacementPayload
	if err := json.Unmarshal(f.placementIn(t, environmentID, f.clusterID).DesiredSpec, &payload); err != nil {
		t.Fatalf("decode desired spec: %v", err)
	}
	rendered := payload.AppSpec.ServiceSpec.Deployment.HealthCheck
	wantRendered := converter.ProtoToServiceDeploymentSpec(&deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{HealthCheck: want}},
	}).HealthCheck
	if !reflect.DeepEqual(rendered, wantRendered) {
		t.Fatalf("rendered health check = %+v, want %+v", rendered, wantRendered)
	}
}

func (f *deployFixture) resourceHealthCheck(t *testing.T) *deploymentv1.HealthCheckConfig {
	t.Helper()
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	spec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		t.Fatalf("decode resource spec: %v", err)
	}
	return spec.GetService().GetHealthCheck()
}
