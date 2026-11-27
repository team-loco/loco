package service

import (
	"bytes"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestUpdateResourceSetsTheFailurePolicyWithoutDeploying(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	imageDeployment(t, f)
	f.finishRollouts(t)
	before := f.placement(t, f.clusterID)
	count := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	server := NewResourceServer(
		f.pool,
		f.queries,
		testServiceDefaults(),
		testClusterSilentAfter,
		limits.Defaults(),
		nil,
	)
	ctx := f.rolloutContext(t)
	workspaceID := f.workspaceID(t)
	reader := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	read := func() *deploymentv1.FailurePolicy {
		t.Helper()
		got, err := getResourceByName(t, f, reader, workspaceID, "svc")
		if err != nil {
			t.Fatal(err)
		}
		return got.GetFailurePolicy()
	}
	defaults := testServiceDefaults()
	if got, want := read(), defaults.FailurePolicy(nil, nil, nil).Proto(); !proto.Equal(got, want) {
		t.Fatalf("policy = %v, want the defaults %v", got, want)
	}

	never := &deploymentv1.FailurePolicy{
		RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_NEVER, DeployTimeoutSeconds: 300,
	}
	if _, err := server.UpdateResource(ctx, &resourcev1.UpdateResourceRequest{
		ResourceId: f.resourceID.String(), FailurePolicy: never,
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); !proto.Equal(got, never) {
		t.Fatalf("policy = %v, want %v", got, never)
	}
	if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); got != count {
		t.Fatalf("deployments = %d, want %d", got, count)
	}
	if after := f.placement(t, f.clusterID); after.DesiredRevision != before.DesiredRevision ||
		!bytes.Equal(after.DesiredSpec, before.DesiredSpec) {
		t.Fatal("a failure policy change modified the placement")
	}

	if _, err := deployService(t, f, imageService()); err != nil {
		t.Fatal(err)
	}
	spec := desiredPayload(t, f).AppSpec.ServiceSpec
	if spec.RestartPolicy.Mode != locoControllerV1.RestartNever || spec.DeployTimeoutSeconds != 300 {
		t.Fatalf("deployed policy = %+v, timeout %d, want never and 300", spec.RestartPolicy, spec.DeployTimeoutSeconds)
	}

	for name, invalid := range map[string]*deploymentv1.FailurePolicy{
		"unset restart policy": {DeployTimeoutSeconds: 300},
		"a budget under never": {
			RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_NEVER, MaxRestarts: 3, DeployTimeoutSeconds: 300,
		},
		"an unknown restart policy": {RestartPolicy: deploymentv1.RestartPolicy(99), DeployTimeoutSeconds: 300},
		"no budget under on-failure": {
			RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_ON_FAILURE, DeployTimeoutSeconds: 300,
		},
		"six restarts": {
			RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_ON_FAILURE, MaxRestarts: 6, DeployTimeoutSeconds: 300,
		},
		"a short timeout": {
			RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_ALWAYS, DeployTimeoutSeconds: 30,
		},
		"a long timeout": {
			RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_ALWAYS, DeployTimeoutSeconds: 3601,
		},
	} {
		_, err := server.UpdateResource(ctx, &resourcev1.UpdateResourceRequest{
			ResourceId: f.resourceID.String(), FailurePolicy: invalid,
		})
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: error = %v, want invalid argument", name, err)
		}
	}
	if got := read(); !proto.Equal(got, never) {
		t.Fatalf("a rejected update changed the policy to %v", got)
	}
}
