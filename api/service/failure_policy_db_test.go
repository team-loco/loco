package service

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

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
	originalID := imageDeployment(t, f)
	original := f.getDeployment(t, originalID.String())
	f.finishRollouts(t)
	staging := f.addStagingEnvironment(t)
	if _, err := deployServiceIn(t, f, imageService(), staging); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	stagingBefore := testResourceConfiguration(t, f.queries, f.resourceID, staging)
	identityBefore, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	before := f.placement(t, f.clusterID)
	count := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	ctx := f.rolloutContext(t)
	workspaceID := f.workspaceID(t)
	reader := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	read := func() *deploymentv1.FailurePolicy {
		t.Helper()
		got, readErr := getResourceByName(t, f, reader, workspaceID, "svc")
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, configuration := range got.GetEnvironments() {
			if configuration.GetEnvironmentId() == f.envID.String() {
				return configuration.GetFailurePolicy()
			}
		}
		t.Fatal("environment configuration missing")
		return nil
	}
	defaults := testServiceDefaults()
	if got, want := read(), defaults.FailurePolicy(nil, nil, nil).Proto(); !proto.Equal(got, want) {
		t.Fatalf("policy = %v, want the defaults %v", got, want)
	}

	never := &deploymentv1.FailurePolicy{
		RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_NEVER, DeployTimeoutSeconds: 300,
	}
	if _, err = server.UpdateResource(ctx, &resourcev1.UpdateResourceRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), FailurePolicy: never,
	}); err != nil {
		t.Fatal(err)
	}
	identityAfter, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(identityAfter, identityBefore) {
		t.Fatal("policy edit changed shared identity")
	}

	if after := testResourceConfiguration(
		t,
		f.queries,
		f.resourceID,
		staging,
	); !reflect.DeepEqual(
		after,
		stagingBefore,
	) {
		t.Fatal("production failure policy update changed staging configuration")
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

	if err = scaleResource(t, f, 2); err != nil {
		t.Fatal(err)
	}
	scaled := desiredPayload(t, f).AppSpec.ServiceSpec
	if scaled.RestartPolicy.Mode != locoControllerV1.RestartNever || scaled.DeployTimeoutSeconds != 300 {
		t.Fatalf("scale kept the previous snapshot policy: %+v", scaled)
	}
	f.finishRollouts(t)
	if _, err = deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), Regions: []string{testRegion},
		Spec: proto.CloneOf(original.GetSpec()),
	}); err != nil {
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
		_, err = server.UpdateResource(ctx, &resourcev1.UpdateResourceRequest{
			ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), FailurePolicy: invalid,
		})
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: error = %v, want invalid argument", name, err)
		}
	}
	if got := read(); !proto.Equal(got, never) {
		t.Fatalf("a rejected update changed the policy to %v", got)
	}
}

func TestCancelRestoresTheSnapshotsPolicyAfterConfigurationAndRegionsChange(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	previous, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	original := f.getDeployment(t, previous.GetDeploymentId())
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	nextPolicy := &deploymentv1.FailurePolicy{
		RestartPolicy:        deploymentv1.RestartPolicy_RESTART_POLICY_NEVER,
		DeployTimeoutSeconds: 300,
	}
	if _, err = server.UpdateResource(
		f.rolloutContext(t),
		&resourcev1.UpdateResourceRequest{
			ResourceId:    f.resourceID.String(),
			EnvironmentId: f.envID.String(),
			FailurePolicy: nextPolicy,
		},
	); err != nil {
		t.Fatal(err)
	}
	next, err := deployService(t, f, imageService())
	if err != nil {
		t.Fatal(err)
	}
	if got := desiredPayload(
		t,
		f,
	).AppSpec.ServiceSpec; got.RestartPolicy.Mode != locoControllerV1.RestartNever ||
		got.DeployTimeoutSeconds != 300 {
		t.Fatalf("next rollout policy = %+v", got)
	}
	// The old region is no longer in desired configuration when cancellation restores its snapshot.
	if _, err = f.queries.UpdateResourceEnvironmentSpec(
		t.Context(),
		genDb.UpdateResourceEnvironmentSpecParams{
			ResourceID:    f.resourceID,
			EnvironmentID: f.envID,
			Spec: []byte(
				`{"routing":{"port":9090},"regions":{"us-va-1":{"enabled":true,"cpu":"1","memory":"1Gi"}}}`,
			),
		},
	); err != nil {
		t.Fatal(err)
	}
	if err = f.cancelDeployment(t, next.GetDeploymentId()); err != nil {
		t.Fatal(err)
	}
	defaults := testServiceDefaults().FailurePolicy(nil, nil, nil)
	restored := desiredPayload(t, f).AppSpec.ServiceSpec
	if restored.RestartPolicy != defaults.Restart || restored.DeployTimeoutSeconds != defaults.DeployTimeoutSeconds {
		t.Fatalf(
			"restored policy = %+v, timeout %d; want original %+v",
			restored.RestartPolicy,
			restored.DeployTimeoutSeconds,
			defaults,
		)
	}
	if restored.Deployment.Port == nil || *restored.Deployment.Port != 8080 || restored.Resources.CPU != testRegionCPU {
		t.Fatalf("restored runtime = %+v, want original port and size", restored)
	}
	if after := f.getDeployment(t, previous.GetDeploymentId()); !proto.Equal(after.GetSpec(), original.GetSpec()) {
		t.Fatal("rollback changed the original deployment snapshot")
	}
}

func TestFailurePolicyUpdateDoesNotWaitForAnotherEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	staging := f.addStagingEnvironment(t)
	if _, err := f.queries.UpsertResourceEnvironment(
		t.Context(),
		genDb.UpsertResourceEnvironmentParams{
			ResourceID:    f.resourceID,
			EnvironmentID: staging,
			WorkspaceID:   f.workspaceID(t),
			Spec:          []byte(sameRegionSpec),
			SpecVersion:   specVersion,
		},
	); err != nil {
		t.Fatal(err)
	}
	stagingBefore := testResourceConfiguration(t, f.queries, f.resourceID, staging)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Error(rollbackErr)
		}
	})
	if _, err = lockEnvironment(t.Context(), f.queries.WithTx(tx), staging); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.rolloutContext(t), 5*time.Second)
	defer cancel()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	policy := &deploymentv1.FailurePolicy{
		RestartPolicy:        deploymentv1.RestartPolicy_RESTART_POLICY_NEVER,
		DeployTimeoutSeconds: 300,
	}
	if _, err = server.UpdateResource(
		ctx,
		&resourcev1.UpdateResourceRequest{
			ResourceId:    f.resourceID.String(),
			EnvironmentId: f.envID.String(),
			FailurePolicy: policy,
		},
	); err != nil {
		t.Fatalf("production policy edit waited for staging lock: %v", err)
	}
	if after := testResourceConfiguration(
		t,
		f.queries,
		f.resourceID,
		staging,
	); !reflect.DeepEqual(
		after,
		stagingBefore,
	) {
		t.Fatal("policy edit changed locked staging configuration")
	}
}

func TestExplicitReplayUsesItsSnapshotWithoutMutableEnvironmentDefaults(t *testing.T) {
	t.Parallel()
	triggers := []deploymentv1.DeploymentTrigger{
		deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_REDEPLOY,
		deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_ROLLBACK,
	}
	for _, trigger := range triggers {
		for _, regions := range []string{"same region", "original region removed"} {
			t.Run(trigger.String()+"/"+regions, func(t *testing.T) {
				t.Parallel()
				f := newDeployFixture(t)
				created, err := createTestDeployment(t, f, sameRegionSpec)
				if err != nil {
					t.Fatal(err)
				}
				f.finishRollouts(t)
				original := f.getDeployment(t, created.GetDeploymentId())
				if original.GetSpec().GetService().GetHealthCheck() != nil {
					t.Fatal("fixture must start without a health check")
				}
				changed := checkedRegionSpec
				if regions == "original region removed" {
					changed = strings.ReplaceAll(changed, testRegion, otherRegion)
				}
				if _, err = f.queries.UpdateResourceEnvironmentSpec(
					t.Context(),
					genDb.UpdateResourceEnvironmentSpecParams{
						ResourceID:    f.resourceID,
						EnvironmentID: f.envID,
						Spec:          []byte(changed),
					},
				); err != nil {
					t.Fatal(err)
				}
				server := NewResourceServer(
					f.pool,
					f.queries,
					testServiceDefaults(),
					testClusterSilentAfter,
					limits.Defaults(),
				)
				policy := &deploymentv1.FailurePolicy{
					RestartPolicy:        deploymentv1.RestartPolicy_RESTART_POLICY_NEVER,
					DeployTimeoutSeconds: 300,
				}
				if _, err = server.UpdateResource(
					f.rolloutContext(t),
					&resourcev1.UpdateResourceRequest{
						ResourceId:    f.resourceID.String(),
						EnvironmentId: f.envID.String(),
						FailurePolicy: policy,
					},
				); err != nil {
					t.Fatal(err)
				}
				request := &deploymentv1.CreateDeploymentRequest{
					ResourceId:         f.resourceID.String(),
					EnvironmentId:      f.envID.String(),
					Regions:            []string{testRegion},
					Trigger:            trigger,
					SourceDeploymentId: original.GetId(),
				}
				replayed, err := deployRequest(t, f, request)
				if err != nil {
					t.Fatalf("copy immutable snapshot after desired configuration changed: %v", err)
				}
				snapshot := f.getDeployment(t, replayed.GetDeploymentId()).GetSpec()
				if got := snapshot.GetService().GetHealthCheck(); got != nil {
					t.Fatalf("replay inherited new desired health check: %v", got)
				}
				if !proto.Equal(
					snapshot.GetService().GetFailurePolicy(),
					original.GetSpec().GetService().GetFailurePolicy(),
				) {
					t.Fatal("replay inherited desired failure policy")
				}
				if !proto.Equal(snapshot, original.GetSpec()) {
					t.Fatalf("replay snapshot = %v, want original %v", snapshot, original.GetSpec())
				}
			})
		}
	}
}
