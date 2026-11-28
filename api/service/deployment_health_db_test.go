package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	checkedRegionSpec = `{"routing":{"port":8080},"healthCheck":{"path":"/stored","initialDelaySeconds":7,` +
		`"intervalSeconds":10,"timeoutSeconds":2,"failureThreshold":3},"regions":{"us-ca-1":{"enabled":true,` +
		`"cpu":"100m","memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	resourceUpdatedEvents = `SELECT count(*) FROM events WHERE type = 'resource.updated' AND subject_id = $1`
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
			created, err := deployRequest(t, f, healthRequest(f, f.envID, service, tc.remove))
			if err != nil {
				t.Fatalf("create deployment: %v", err)
			}
			f.wantDeployedHealth(t, f.envID, created, tc.wantDeployed)
			if got := f.resourceHealthCheck(t); !proto.Equal(got, tc.wantResource) {
				t.Fatalf("resource health check = %v, want %v", got, tc.wantResource)
			}

			f.finishRollouts(t)
			again, err := deployService(t, f, imageService())
			if err != nil {
				t.Fatalf("deploy again: %v", err)
			}
			f.wantDeployedHealth(t, f.envID, again, tc.wantResource)
		})
	}
}

func TestCreateDeploymentRemovingAHealthCheckChangesOnlyItsEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, checkedRegionSpec)
	staging := f.addStagingEnvironment(t)
	if _, err := f.queries.UpsertResourceEnvironment(
		t.Context(),
		genDb.UpsertResourceEnvironmentParams{
			ResourceID:    f.resourceID,
			EnvironmentID: staging,
			WorkspaceID:   f.workspaceID(t),
			Spec:          []byte(checkedRegionSpec),
			SpecVersion:   specVersion,
		},
	); err != nil {
		t.Fatal(err)
	}
	stagingConfig := testResourceConfiguration(t, f.queries, f.resourceID, staging)
	stagingDeployment, err := deployServiceIn(t, f, imageService(), staging)
	if err != nil {
		t.Fatalf("deploy to staging: %v", err)
	}
	stagingBefore := f.revision(t, staging)
	productionBefore := f.revision(t, f.envID)

	if _, err = deployRequest(t, f, healthRequest(f, f.envID, imageService(), true)); err != nil {
		t.Fatalf("remove health check: %v", err)
	}
	stagingAfter := f.revision(t, staging)
	productionAfter := f.revision(t, f.envID)
	if stagingAfter != stagingBefore || productionAfter <= productionBefore {
		t.Fatalf("revisions staging %d -> %d, production %d -> %d, want only production advanced",
			stagingBefore, stagingAfter, productionBefore, productionAfter)
	}
	if n := f.count(t, resourceUpdatedEvents); n != 1 {
		t.Fatalf("%d resource.updated events, want 1", n)
	}
	if after := testResourceConfiguration(
		t,
		f.queries,
		f.resourceID,
		staging,
	); !reflect.DeepEqual(
		after,
		stagingConfig,
	) {
		t.Fatal("removing production health check changed staging config")
	}
	f.wantDeployedHealth(t, staging, stagingDeployment, storedHealthCheck())
}

func TestCreateDeploymentRemovingAnAbsentHealthCheckLeavesTheResource(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, sameRegionSpec)
	staging := f.addStagingEnvironment(t)
	before := testResourceConfiguration(t, f.queries, f.resourceID, f.envID).Revision
	stagingBefore := f.revision(t, staging)
	productionBefore := f.revision(t, f.envID)
	created, err := deployRequest(t, f, healthRequest(f, f.envID, imageService(), true))
	if err != nil {
		t.Fatalf("remove health check: %v", err)
	}
	if after := testResourceConfiguration(t, f.queries, f.resourceID, f.envID).Revision; after != before {
		t.Fatalf("resource revision %d -> %d, want unchanged", before, after)
	}
	if f.revision(t, staging) != stagingBefore {
		t.Fatal("removing an absent check advanced staging's revision")
	}
	if f.revision(t, f.envID) <= productionBefore {
		t.Fatal("the deployment did not advance production's revision")
	}
	if n := f.count(t, resourceUpdatedEvents); n != 0 {
		t.Fatalf("%d resource.updated events, want 0", n)
	}
	f.wantDeployedHealth(t, f.envID, created, nil)
}

func TestCreateDeploymentRejectedRemovalKeepsTheHealthCheck(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	seedDeployment(t, f, checkedRegionSpec)
	staging := f.addStagingEnvironment(t)
	stagingBefore := f.revision(t, staging)
	productionBefore := f.revision(t, f.envID)
	resourceBefore := testResourceConfiguration(t, f.queries, f.resourceID, f.envID).Revision
	service := imageService()
	testTarget(service).Cpu = new("lots")
	request := healthRequest(f, f.envID, service, true)
	if err := protovalidate.Validate(request); err != nil {
		t.Fatalf("the request must pass wire validation and fail after the removal: %v", err)
	}
	_, err := deployRequest(t, f, request)
	wantCode(t, err, connect.CodeInvalidArgument)
	if got := f.resourceHealthCheck(t); !proto.Equal(got, storedHealthCheck()) {
		t.Fatalf("resource health check = %v, want %v", got, storedHealthCheck())
	}
	if f.revision(t, staging) != stagingBefore || f.revision(t, f.envID) != productionBefore {
		t.Fatal("a rejected deployment advanced an environment's revision")
	}
	if testResourceConfiguration(t, f.queries, f.resourceID, f.envID).Revision != resourceBefore {
		t.Fatal("a rejected deployment advanced the resource's revision")
	}
	if n := f.count(t, resourceUpdatedEvents); n != 0 {
		t.Fatalf("%d resource.updated events after a rejected deployment, want 0", n)
	}
}

func TestCreateDeploymentRejectsRemovingAHealthCheckItSets(t *testing.T) {
	t.Parallel()
	checked := imageService()
	checked.HealthCheck = workerHealthCheck()
	for name, tc := range map[string]struct {
		service *deploymentv1.ServiceDeploymentSpec
		remove  bool
		valid   bool
	}{
		"neither":                {service: imageService(), valid: true},
		"a check":                {service: checked, valid: true},
		"remove without a check": {service: imageService(), remove: true, valid: true},
		"remove with a check":    {service: checked, remove: true, valid: false},
	} {
		t.Run(name, func(t *testing.T) {
			err := protovalidate.Validate(wireRequest(tc.service, tc.remove))
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

func wireRequest(service *deploymentv1.ServiceDeploymentSpec, remove bool) *deploymentv1.CreateDeploymentRequest {
	return &deploymentv1.CreateDeploymentRequest{
		ResourceId:        "01a1dbb5-4966-72e2-a92b-a2560006942a",
		EnvironmentId:     "01a1dbb5-4966-72e2-a92b-a2560006942b",
		Spec:              &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
		RemoveHealthCheck: remove,
	}
}

// wantDeployedHealth checks the created deployment's health check, as the API returns it and as
// the environment's placement, which runs that deployment, renders it.
func (f *deployFixture) wantDeployedHealth(
	t *testing.T,
	environmentID uuid.UUID,
	created *deploymentv1.CreateDeploymentResponse,
	want *deploymentv1.HealthCheckConfig,
) {
	t.Helper()
	deployment := f.getDeployment(t, created.GetDeploymentId())
	if got := deployment.GetSpec().GetService().GetHealthCheck(); !proto.Equal(got, want) {
		t.Fatalf("stored health check = %v, want %v", got, want)
	}
	var payload locoControllerV1.PlacementPayload
	if err := json.Unmarshal(f.placementIn(t, environmentID, f.clusterID).DesiredSpec, &payload); err != nil {
		t.Fatalf("decode desired spec: %v", err)
	}
	if payload.AppSpec.DeploymentID != created.GetDeploymentId() {
		t.Fatalf("placement runs %s, want %s", payload.AppSpec.DeploymentID, created.GetDeploymentId())
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
	spec, err := converter.DeserializeResourceSpec(
		testResourceConfiguration(t, f.queries, resource.ID, f.envID).Spec,
		resource.Type,
	)
	if err != nil {
		t.Fatalf("decode resource spec: %v", err)
	}
	return spec.GetService().GetHealthCheck()
}
