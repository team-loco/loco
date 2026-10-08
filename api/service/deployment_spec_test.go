package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const testImage = "registry.loco.test/app@sha256:" +
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const (
	testRegion        = "us-east-1"
	testDefaultCPU    = "150m"
	testDefaultMemory = "192Mi"
	testIdleTimeout   = 45
	testRegionCPU     = "100m"
	testRegionMemory  = "64Mi"
	testHostname      = "svc.loco.test"
)

func testServiceDefaults() servicedefaults.Defaults {
	return servicedefaults.Defaults{
		CPU:         testDefaultCPU,
		Memory:      testDefaultMemory,
		MinReplicas: 2,
		MaxReplicas: 3,
		PathPrefix:  "/",
		IdleTimeout: testIdleTimeout,
	}
}

func testResourceSpec() *resourcev1.ResourceSpec {
	region := &resourcev1.RegionTarget{
		Enabled:     true,
		Primary:     true,
		Cpu:         testRegionCPU,
		Memory:      testRegionMemory,
		MinReplicas: 1,
		MaxReplicas: 1,
	}
	service := &resourcev1.ServiceSpec{Regions: map[string]*resourcev1.RegionTarget{"us-east-1": region}}
	return &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: service}}
}

func testDeploymentSpec(port int32) *deploymentv1.DeploymentSpec {
	build := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testImage}
	service := &deploymentv1.ServiceDeploymentSpec{Build: build, Port: port}
	return &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}}
}

func testServiceResource() genDb.Resource {
	return genDb.Resource{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Name:        "svc",
		Type:        genDb.ResourceTypeService,
	}
}

func mergedTestSpec(
	t *testing.T,
	resourceSpec *resourcev1.ResourceSpec,
	requestSpec *deploymentv1.DeploymentSpec,
) *deploymentv1.DeploymentSpec {
	t.Helper()
	merged, err := converter.MergeDeploymentSpec(resourceSpec, requestSpec, testRegion, testServiceDefaults())
	if err != nil {
		t.Fatalf("MergeDeploymentSpec: %v", err)
	}
	return merged
}

func testDesiredSpecFrom(
	t *testing.T,
	resourceSpec *resourcev1.ResourceSpec,
	requestSpec *deploymentv1.DeploymentSpec,
) desiredSpecFunc {
	t.Helper()
	return testDesiredSpecAt(t, testHostname, resourceSpec, requestSpec)
}

func testDesiredSpecAt(
	t *testing.T,
	hostname string,
	resourceSpec *resourcev1.ResourceSpec,
	requestSpec *deploymentv1.DeploymentSpec,
) desiredSpecFunc {
	t.Helper()
	merged := mergedTestSpec(t, resourceSpec, requestSpec)
	return desiredApplicationSpec(
		testServiceResource(),
		resourceSpec,
		hostname,
		merged,
		testRegion,
		uuid.New(),
		"prod",
		testServiceDefaults(),
	)
}

func testDesiredSpec(t *testing.T, port int32) desiredSpecFunc {
	t.Helper()
	resourceSpec := testResourceSpec()
	requestSpec := testDeploymentSpec(port)
	return testDesiredSpecFrom(t, resourceSpec, requestSpec)
}

func TestDesiredSpecAcceptsValidSpec(t *testing.T) {
	buildSpec := testDesiredSpec(t, 8080)
	if _, err := buildSpec(uuid.New()); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
}

func TestDeploymentTxErrorRejectsInvalidSpecAsInvalidArgument(t *testing.T) {
	buildSpec := testDesiredSpec(t, 80)
	_, specErr := buildSpec(uuid.New())
	if specErr == nil {
		t.Fatal("port 80 passed validation")
	}

	joined := errors.Join(errDesiredSpec, specErr)
	txErr := deploymentTxError(context.Background(), joined)
	wantCode(t, txErr, connect.CodeInvalidArgument)
	var connectErr *connect.Error
	if !errors.As(txErr, &connectErr) {
		t.Fatalf("err = %v, want a connect error", txErr)
	}
	want := "invalid deployment spec: invalid deployment: port must be between 1024 and 65535, got 80"
	if msg := connectErr.Message(); msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}
}

func TestDeploymentTxErrorRejectsUnknownRegionAsInvalidArgument(t *testing.T) {
	resourceSpec := testResourceSpec()
	deploymentSpec := mergedTestSpec(t, resourceSpec, testDeploymentSpec(8080))
	buildSpec := desiredApplicationSpec(
		testServiceResource(),
		resourceSpec,
		testHostname,
		deploymentSpec,
		"eu-west-1",
		uuid.New(),
		"prod",
		testServiceDefaults(),
	)
	_, specErr := buildSpec(uuid.New())
	joined := errors.Join(errDesiredSpec, specErr)
	txErr := deploymentTxError(context.Background(), joined)
	wantCode(t, txErr, connect.CodeInvalidArgument)
}

func TestDeploymentTxErrorKeepsOtherSpecFailuresInternal(t *testing.T) {
	boom := errors.New("boom")
	joined := errors.Join(errDesiredSpec, boom)
	txErr := deploymentTxError(context.Background(), joined)
	wantCode(t, txErr, connect.CodeInternal)
}

func TestDeployRejectsInvalidSpecWithoutWritingADeployment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	buildSpec := testDesiredSpec(t, 80)
	_, err := f.deploy(ctx, buildSpec)
	txErr := deploymentTxError(ctx, err)
	wantCode(t, txErr, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after an invalid deploy, want 0", n)
	}
}

func applicationPayload(t *testing.T, buildSpec desiredSpecFunc) ApplicationPayload {
	t.Helper()
	raw, err := buildSpec(uuid.New())
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}
	var payload ApplicationPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return payload
}

func TestDesiredSpecFillsOmittedValuesFromConfiguredDefaults(t *testing.T) {
	resourceSpec := testResourceSpec()
	region := resourceSpec.GetService().GetRegions()[testRegion]
	region.Cpu = ""
	region.Memory = ""
	region.MinReplicas = 0
	region.MaxReplicas = 0
	resourceSpec.GetService().Routing = &resourcev1.RoutingConfig{Port: 8080}

	payload := applicationPayload(t, testDesiredSpecFrom(t, resourceSpec, testDeploymentSpec(8080)))
	service := payload.AppSpec.ServiceSpec
	resources := service.Resources
	if resources.CPU != testDefaultCPU || resources.Memory != testDefaultMemory {
		t.Errorf("cpu %q, memory %q, want the configured %q and %q",
			resources.CPU, resources.Memory, testDefaultCPU, testDefaultMemory)
	}
	if resources.Replicas.Min != 2 || resources.Replicas.Max != 3 {
		t.Errorf("replicas = %+v, want the configured 2 and 3", resources.Replicas)
	}
	if service.Routing.PathPrefix != "/" || service.Routing.IdleTimeout != testIdleTimeout {
		t.Errorf("routing = %+v, want the configured path prefix and idle timeout", service.Routing)
	}
}

func TestDesiredSpecPrefersExplicitValues(t *testing.T) {
	resourceSpec := testResourceSpec()
	resourceSpec.GetService().Routing = &resourcev1.RoutingConfig{PathPrefix: "/api", IdleTimeout: 120}
	requestSpec := testDeploymentSpec(8080)
	request := requestSpec.GetService()
	cpu := "250m"
	maxReplicas := int32(2)
	request.Cpu = &cpu
	request.MaxReplicas = &maxReplicas

	payload := applicationPayload(t, testDesiredSpecFrom(t, resourceSpec, requestSpec))
	service := payload.AppSpec.ServiceSpec
	resources := service.Resources
	if resources.CPU != cpu {
		t.Errorf("cpu = %q, want the request's %q", resources.CPU, cpu)
	}
	if resources.Memory != testRegionMemory || resources.Replicas.Min != 1 {
		t.Errorf("memory %q, min replicas %d, want the region's 64Mi and 1", resources.Memory, resources.Replicas.Min)
	}
	if resources.Replicas.Max != maxReplicas {
		t.Errorf("max replicas = %d, want the request's %d", resources.Replicas.Max, maxReplicas)
	}
	if service.Routing.PathPrefix != "/api" || service.Routing.IdleTimeout != 120 {
		t.Errorf("routing = %+v, want the resource's /api and 120", service.Routing)
	}
}

func TestDesiredSpecRoutesTheHostnameWithDefaultsWhenRoutingIsUnset(t *testing.T) {
	buildSpec := testDesiredSpec(t, 8080)
	payload := applicationPayload(t, buildSpec)
	want := &locoControllerV1.RoutingSpec{HostName: testHostname, PathPrefix: "/", IdleTimeout: testIdleTimeout}
	if got := payload.AppSpec.ServiceSpec.Routing; got == nil || *got != *want {
		t.Fatalf("routing = %+v, want %+v", got, want)
	}
}

func TestDesiredSpecWithoutAHostnameHasNoRouting(t *testing.T) {
	resourceSpec := testResourceSpec()
	resourceSpec.GetService().Routing = &resourcev1.RoutingConfig{Port: 8080}

	requestSpec := testDeploymentSpec(8080)
	buildSpec := testDesiredSpecAt(t, "", resourceSpec, requestSpec)
	payload := applicationPayload(t, buildSpec)
	if routing := payload.AppSpec.ServiceSpec.Routing; routing != nil {
		t.Fatalf("routing = %+v, want none without a hostname", routing)
	}
}

func TestValidateQuantitiesRejectsInvalidValues(t *testing.T) {
	valid := mergedTestSpec(t, testResourceSpec(), testDeploymentSpec(8080)).GetService()
	if err := validateQuantities(valid); err != nil {
		t.Fatalf("valid quantities rejected: %v", err)
	}

	badCPU := "lots"
	invalidCPU := mergedTestSpec(t, testResourceSpec(), testDeploymentSpec(8080)).GetService()
	invalidCPU.Cpu = &badCPU
	if err := validateQuantities(invalidCPU); !errors.Is(err, servicedefaults.ErrInvalidCPU) {
		t.Errorf("cpu %q = %v, want ErrInvalidCPU", badCPU, err)
	}

	badMemory := "a lot"
	invalidMemory := mergedTestSpec(t, testResourceSpec(), testDeploymentSpec(8080)).GetService()
	invalidMemory.Memory = &badMemory
	if err := validateQuantities(invalidMemory); !errors.Is(err, servicedefaults.ErrInvalidMemory) {
		t.Errorf("memory %q = %v, want ErrInvalidMemory", badMemory, err)
	}
}

func TestDeployRejectsAnOutOfRangeQuantityAsInvalidArgument(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	requestSpec := testDeploymentSpec(8080)
	cpu := "64"
	requestSpec.GetService().Cpu = &cpu
	buildSpec := testDesiredSpecFrom(t, testResourceSpec(), requestSpec)
	_, err := f.deploy(ctx, buildSpec)
	txErr := deploymentTxError(ctx, err)
	wantCode(t, txErr, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after an invalid deploy, want 0", n)
	}
}
