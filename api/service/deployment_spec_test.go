package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const testImage = "registry.loco.test/app@sha256:" +
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testResourceSpec() *resourcev1.ResourceSpec {
	region := &resourcev1.RegionTarget{
		Enabled:     true,
		Primary:     true,
		Cpu:         "100m",
		Memory:      "64Mi",
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

func testDesiredSpec(port int32) desiredSpecFunc {
	resource := genDb.Resource{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		Name:        "svc",
		Type:        genDb.ResourceTypeService,
	}
	resourceSpec := testResourceSpec()
	deploymentSpec := testDeploymentSpec(port)
	return desiredApplicationSpec(
		resource,
		resourceSpec,
		"svc.loco.test",
		deploymentSpec,
		"us-east-1",
		uuid.New(),
		"prod",
	)
}

func TestDesiredSpecAcceptsValidSpec(t *testing.T) {
	buildSpec := testDesiredSpec(8080)
	if _, err := buildSpec(uuid.New()); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
}

func TestDeploymentTxErrorRejectsInvalidSpecAsInvalidArgument(t *testing.T) {
	buildSpec := testDesiredSpec(80)
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
	resource := genDb.Resource{ID: uuid.New(), WorkspaceID: uuid.New(), Type: genDb.ResourceTypeService}
	resourceSpec := testResourceSpec()
	deploymentSpec := testDeploymentSpec(8080)
	buildSpec := desiredApplicationSpec(
		resource,
		resourceSpec,
		"svc.loco.test",
		deploymentSpec,
		"eu-west-1",
		uuid.New(),
		"prod",
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

	buildSpec := testDesiredSpec(80)
	_, err := f.deploy(ctx, buildSpec)
	txErr := deploymentTxError(ctx, err)
	wantCode(t, txErr, connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments after an invalid deploy, want 0", n)
	}
}
