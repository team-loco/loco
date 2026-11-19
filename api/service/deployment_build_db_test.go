package service

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func deployBuild(t *testing.T, f *buildFixture, buildID string) uuid.UUID {
	t.Helper()
	seedDeployment(t, f.deployFixture, sameRegionSpec)
	response, err := deployService(t, f.deployFixture, &deploymentv1.ServiceDeploymentSpec{
		Build: &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(response.GetDeploymentId())
}

func TestDeploymentWaitsForBuild(t *testing.T) {
	t.Parallel()
	for _, initial := range []genDb.BuildStatus{
		genDb.BuildStatusAwaitingUpload,
		genDb.BuildStatusQueued,
		genDb.BuildStatusRunning,
	} {
		t.Run(string(initial), func(t *testing.T) {
			f := newBuildFixture(t)
			buildID := f.create(t, 10).GetBuildId()
			if initial != genDb.BuildStatusAwaitingUpload {
				f.uploadFor(t, buildID, 10)
				if _, err := f.start(buildID); err != nil {
					t.Fatal(err)
				}
			}
			if initial == genDb.BuildStatusRunning {
				status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_RUNNING)
				if err := f.agentServer().
					recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
					t.Fatal(err)
				}
			}
			id := deployBuild(t, f, buildID)
			if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusBuilding {
				t.Fatalf("status=%s", got)
			}
			if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id=$1`); n != 0 {
				t.Fatalf("placements=%d", n)
			}
			if initial == genDb.BuildStatusAwaitingUpload {
				f.uploadFor(t, buildID, 10)
				if _, err := f.start(buildID); err != nil {
					t.Fatal(err)
				}
			}
			status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
			status.ImageDigest = testImageDigest
			if err := f.agentServer().
				recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
				t.Fatal(err)
			}
			if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusDeploying {
				t.Fatalf("status=%s", got)
			}
			p := f.placement(t, f.clusterID)
			if p.DeploymentID == nil || *p.DeploymentID != id {
				t.Fatalf("placement deployment=%v", p.DeploymentID)
			}
			payload := desiredPayload(t, f.deployFixture)
			if got := payload.AppSpec.ServiceSpec.Deployment.Image; got != f.row(
				t,
				buildID,
			).ImageRepository+"@"+testImageDigest {
				t.Fatalf("image=%s", got)
			}
		})
	}
}

func TestDeploymentBuildPreservesEnvironment(t *testing.T) {
	t.Parallel()
	for _, inherit := range []bool{false, true} {
		t.Run(map[bool]string{false: "requested", true: "inherited"}[inherit], func(t *testing.T) {
			f := newBuildFixture(t)
			seedDeployment(t, f.deployFixture, sameRegionSpec)
			provider := newTestKeyProvider(t, "k1")
			f.storeSecret(t, provider, databaseURLName, stripeValue)
			f.storeSecret(t, provider, sessionKeyName, sessionKeyValue)
			wantEnv := map[string]string{"BUILD_MODE": "release", "BUILD_LABEL": "queued-build"}
			bindings := &deploymentv1.DeploymentSecretBindings{Names: testSecretNames}
			request := &deploymentv1.CreateDeploymentRequest{
				ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), Regions: []string{testRegion},
				SecretBindings: bindings,
			}
			if inherit {
				previous := imageService()
				previous.Env = map[string]string{"BUILD_LABEL": "earlier"}
				request.Spec = &deploymentv1.DeploymentSpec{
					Spec: &deploymentv1.DeploymentSpec_Service{Service: previous},
				}
				if _, err := deployRequest(t, f.deployFixture, request); err != nil {
					t.Fatal(err)
				}
				f.finishRollouts(t)
				request.SecretBindings = nil
			}
			buildID := f.queued(t)
			service := &deploymentv1.ServiceDeploymentSpec{
				Build: &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID},
				Env:   wantEnv,
			}
			request.Spec = &deploymentv1.DeploymentSpec{
				Spec: &deploymentv1.DeploymentSpec_Service{Service: service},
			}
			response, err := deployRequest(t, f.deployFixture, request)
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.MustParse(response.GetDeploymentId())
			if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusBuilding {
				t.Fatalf("status=%s", got)
			}
			view, err := f.queries.GetDeploymentView(f.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := deploymentToProto(view)
			if err != nil {
				t.Fatal(err)
			}
			if got := deployment.GetSpec().GetService().GetEnv(); !maps.Equal(got, wantEnv) {
				t.Errorf("deployment spec env=%v, want %v", got, wantEnv)
			}
			status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
			status.ImageDigest = testImageDigest
			if statusErr := f.agentServer().
				recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); statusErr != nil {
				t.Fatal(statusErr)
			}
			if got := desiredPayload(t, f.deployFixture).AppSpec.ServiceSpec.Deployment.Env; !maps.Equal(got, wantEnv) {
				t.Errorf("env=%v, want %v", got, wantEnv)
			}
			if got := f.placement(t, f.clusterID).SecretNames; !slices.Equal(got, testSecretNames) {
				t.Errorf("secret names=%v, want %v", got, testSecretNames)
			}
			stored, err := f.queries.GetDeploymentByID(f.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			rollback, err := converter.DeserializeDeploymentSpec(stored.Spec, genDb.ResourceTypeService)
			if err != nil {
				t.Fatal(err)
			}
			latest := imageService()
			latest.Env = map[string]string{"BUILD_MODE": "updated"}
			f.finishRollouts(t)
			if _, err := deployService(t, f.deployFixture, latest); err != nil {
				t.Fatal(err)
			}
			f.finishRollouts(t)
			if _, err := deployService(t, f.deployFixture, rollback.GetService()); err != nil {
				t.Fatal(err)
			}
			got := desiredPayload(t, f.deployFixture).AppSpec.ServiceSpec.Deployment.Env
			if !maps.Equal(got, wantEnv) {
				t.Errorf("rollback env=%v, want %v", got, wantEnv)
			}
		})
	}
}

func TestDeploymentBuildOutcomes(t *testing.T) {
	t.Parallel()
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "canceled"}[canceled], func(t *testing.T) {
			f := newBuildFixture(t)
			buildID := f.queued(t)
			id := deployBuild(t, f, buildID)
			if canceled {
				if _, err := f.cancel(buildID); err != nil {
					t.Fatal(err)
				}
			} else {
				status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_FAILED)
				status.Message = "build stage failed: exit code 1"
				if err := f.agentServer().
					recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
					t.Fatal(err)
				}
				row, err := f.queries.GetDeploymentView(f.ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				deployment, err := deploymentToProto(row)
				if err != nil {
					t.Fatal(err)
				}
				if problem := deployment.GetPlacements()[0].GetProblem(); problem.GetReason() != "build_failed" ||
					problem.GetDetail() != status.GetMessage() {
					t.Fatalf("problem=%v", problem)
				}
			}
			want := genDb.DeploymentStatusFailed
			if canceled {
				want = genDb.DeploymentStatusCanceled
			}
			if got := f.deploymentStatus(t, id); got != want {
				t.Fatalf("status=%s", got)
			}
			if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id=$1`); n != 0 {
				t.Fatalf("placements=%d", n)
			}
		})
	}
}

func TestDeploymentBuildActivity(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	id := deployBuild(t, f, buildID)
	conn, acquireErr := f.pool.Acquire(f.ctx)
	if acquireErr != nil {
		t.Fatal(acquireErr)
	}
	defer conn.Release()
	if _, err := conn.Exec(f.ctx, "LISTEN deployment_activity"); err != nil {
		t.Fatal(err)
	}
	stages := []string{"Fetch", "Build", "Push"}
	stageTimes := make([]time.Time, 0, len(stages))
	for _, stage := range stages {
		since := time.Now().Add(-time.Second).Truncate(time.Microsecond)
		stageTimes = append(stageTimes, since)
		status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_RUNNING)
		status.Stage, status.StageSince = stage, timestamppb.New(since)
		for range 2 {
			if err := f.agentServer().
				recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
				t.Fatal(err)
			}
			status.StageSince = timestamppb.New(since.Add(time.Second))
		}
	}
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	notification, err := conn.Conn().WaitForNotification(ctx)
	if err != nil || notification.Payload != id.String() {
		t.Fatalf("notification=%v %v", notification, err)
	}
	rows, err := f.queries.ListDeploymentActivity(
		f.ctx,
		genDb.ListDeploymentActivityParams{DeploymentID: id, PageSize: 200},
	)
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]string, 0, len(rows))
	for i, row := range rows {
		if row.Seq != int64(i+1) {
			t.Fatalf("seq=%d", row.Seq)
		}
		messages = append(messages, row.Message)
		if i < len(stageTimes) && !row.At.Equal(stageTimes[i]) {
			t.Fatalf("%s at %v, want %v", row.Message, row.At, stageTimes[i])
		}
	}
	if want := []string{
		"Fetching source",
		"Building image",
		"Pushing image",
	}; !slices.Equal(
		messages,
		want,
	) {
		t.Fatalf("messages=%v", messages)
	}
}

func TestNewDeploymentCancelsBuildingDeployment(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	first := deployBuild(t, f, buildID)
	second := deployBuild(t, f, buildID)
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusCanceled {
		t.Fatalf("status=%s", got)
	}
	status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	status.ImageDigest = testImageDigest
	if err := f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
		t.Fatal(err)
	}
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusCanceled {
		t.Fatalf("replaced status=%s", got)
	}
	if got := f.deploymentStatus(t, second); got != genDb.DeploymentStatusDeploying {
		t.Fatalf("current status=%s", got)
	}
}

func TestDeploymentBuildSuccessRollsBackWithPlacementFailure(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	id := deployBuild(t, f, buildID)
	if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION reject_placement() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'injected placement failure'; END; $$;
 CREATE TRIGGER reject_placement BEFORE INSERT ON placements
 FOR EACH ROW EXECUTE FUNCTION reject_placement()`); err != nil {
		t.Fatal(err)
	}
	status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	status.ImageDigest = testImageDigest
	if err := f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err == nil {
		t.Fatal("success committed despite placement failure")
	}
	if got := f.row(t, buildID).Status; got != genDb.BuildStatusQueued {
		t.Fatalf("build=%s", got)
	}
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusBuilding {
		t.Fatalf("deployment=%s", got)
	}
}

func TestBuildingDeploymentPreservesPreviousPlacement(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	previous, err := f.deployTo(f.ctx, f.otherCluster, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.otherCluster)
	f.agentServer().recordStatus(f.ctx, f.otherCluster, 0, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision, Available: true, Healthy: true,
	})
	buildID := f.queued(t)
	id := deployBuild(t, f, buildID)
	if old := f.placement(t, f.otherCluster); old.DesiredDeleted || *old.DeploymentID != previous {
		t.Fatalf("previous placement=%+v", old)
	}
	status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	status.ImageDigest = testImageDigest
	if err := f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
		t.Fatal(err)
	}
	if old := f.placement(t, f.otherCluster); !old.DesiredDeleted {
		t.Fatal("previous cluster placement not retired")
	}
	if got := f.deploymentStatus(t, previous); got != genDb.DeploymentStatusRunning {
		t.Fatalf("previous=%s", got)
	}
	if current := f.placement(t, f.clusterID); current.DeploymentID == nil || *current.DeploymentID != id {
		t.Fatalf("current=%+v", current)
	}
}

func TestBuildDeploymentUsesStoredSpecAndCurrentResource(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	deployBuild(t, f, buildID)
	if _, err := f.pool.Exec(f.ctx, `UPDATE resources SET
 spec=jsonb_set(spec,'{regions,us-east-1,cpu}','"900m"') WHERE id=$1`, f.resourceID); err != nil {
		t.Fatal(err)
	}
	status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	status.ImageDigest = testImageDigest
	if err := f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
		t.Fatal(err)
	}
	payload := desiredPayload(t, f.deployFixture)
	if got := payload.AppSpec.ServiceSpec.Resources.CPU; got != testRegionCPU {
		t.Fatalf("cpu=%s", got)
	}
}

func TestBuildFailureProblemSurvivesBuildHistoryPruning(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	id := deployBuild(t, f, buildID)
	status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_FAILED)
	status.Message = "build stage failed: exit code 1"
	if err := f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
		t.Fatal(err)
	}
	if _, err := f.queries.PruneBuildHistory(
		f.ctx,
		genDb.PruneBuildHistoryParams{ResourceID: f.resourceID, Keep: 0},
	); err != nil {
		t.Fatal(err)
	}
	row, err := f.queries.GetDeploymentView(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := deploymentToProto(row)
	if err != nil {
		t.Fatal(err)
	}
	if problem := deployment.GetPlacements()[0].GetProblem(); problem.GetReason() != "build_failed" ||
		problem.GetDetail() != status.GetMessage() ||
		problem.GetSince() == nil {
		t.Fatalf("problem=%v", problem)
	}
}

func TestABuildingDeploymentShowsTheServingReplicas(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	serving := rollbackDeployment(t, f.deployFixture, false, nil)
	p := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(f.ctx, f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), DeploymentId: serving.String(), ObservedRevision: p.DesiredRevision,
		ReadyReplicas: 1, Available: true, Healthy: true,
		Replicas: []*deploymentv1.Replica{{Uid: "serving-pod", DeploymentId: serving.String(), Ready: true,
			State: string(loco.ReplicaServing), PlacementRevision: p.DesiredRevision}},
	})
	building := deployBuild(t, f, f.queued(t))

	view := func(id uuid.UUID) *deploymentv1.DeploymentPlacement {
		t.Helper()
		row, err := f.queries.GetDeploymentView(f.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := deploymentToProto(row)
		if err != nil {
			t.Fatal(err)
		}
		return deployment.GetPlacements()[0]
	}
	desired := view(building)
	if !desired.GetLive() || desired.GetStatus() != deploymentv1.PlacementStatus_PLACEMENT_STATUS_PENDING ||
		len(desired.GetReplicas()) != 1 || desired.GetReplicas()[0].GetDeploymentId() != serving.String() {
		t.Fatalf("building placement = %v, want live and pending with the serving replica", desired)
	}
	if previous := view(serving); previous.GetLive() ||
		previous.GetStatus() != deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING {
		t.Fatalf("serving placement = %v, want running and no longer the region's desired deployment", previous)
	}
	statuses, err := (&ResourceServer{queries: f.queries}).environmentStatuses(f.ctx, []uuid.UUID{f.resourceID})
	if err != nil {
		t.Fatal(err)
	}
	region := statuses[f.resourceID][0].GetRegions()[0]
	if region.GetDeploymentId() != building.String() ||
		region.GetPlacementStatus() != deploymentv1.PlacementStatus_PLACEMENT_STATUS_PENDING ||
		region.GetReadyReplicas() != 1 || len(region.GetReplicas()) != 1 ||
		region.GetReplicas()[0].GetDeploymentId() != serving.String() {
		t.Fatalf("region = %v, want the building deployment pending beside the serving replica", region)
	}
}

func TestAFailedBuildLeavesTheRollingDeploymentLive(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	rolling := rollbackDeployment(t, f.deployFixture, false, nil)
	buildID := f.queued(t)
	deployBuild(t, f, buildID)
	status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_FAILED)
	if err := f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
		t.Fatal(err)
	}
	if got := f.deploymentStatus(t, rolling); got != genDb.DeploymentStatusDeploying {
		t.Fatalf("rolling deployment = %s, want deploying", got)
	}
	statuses, err := (&ResourceServer{queries: f.queries}).environmentStatuses(f.ctx, []uuid.UUID{f.resourceID})
	if err != nil {
		t.Fatal(err)
	}
	if region := statuses[f.resourceID][0].GetRegions()[0]; region.GetDeploymentId() != rolling.String() {
		t.Fatalf("region = %v, want the rolling deployment", region)
	}
}

const buildModeKey = "MODE"

func TestScaleDuringABuildKeepsTheBuildingDeploymentsEnv(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	serving := imageService()
	serving.Env = map[string]string{buildModeKey: "serving"}
	if _, err := createDeploymentWith(t, f.deployFixture, sameRegionSpec, serving); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	buildID := f.queued(t)
	building, err := deployService(t, f.deployFixture, &deploymentv1.ServiceDeploymentSpec{
		Build: &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID},
		Env:   map[string]string{buildModeKey: "building"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = scaleResource(t, f.deployFixture, 2); err != nil {
		t.Fatal(err)
	}
	live, err := f.queries.GetLiveDeploymentForRegion(f.ctx, genDb.GetLiveDeploymentForRegionParams{
		ResourceID: f.resourceID, EnvironmentID: f.envID, Region: testRegion,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := live.Deployment.SourceDeploymentID
	if live.Deployment.ID.String() == building.GetDeploymentId() ||
		source == nil || source.String() != building.GetDeploymentId() {
		t.Fatalf(
			"live deployment = %+v, want a scale copying the building %s",
			live.Deployment,
			building.GetDeploymentId(),
		)
	}
	scaled, err := converter.DeserializeDeploymentSpec(live.Deployment.Spec, genDb.ResourceTypeService)
	if err != nil {
		t.Fatal(err)
	}
	if env := scaled.GetService().GetEnv(); env[buildModeKey] != "building" {
		t.Fatalf("scaled env = %v, want the building deployment's", env)
	}
}

func TestCreateDeploymentRejectsARepeatedRegion(t *testing.T) {
	t.Parallel()
	req := &deploymentv1.CreateDeploymentRequest{
		ResourceId:    uuid.NewString(),
		EnvironmentId: uuid.NewString(),
		Regions:       []string{testRegion, testRegion},
		Spec: &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{
			Service: &deploymentv1.ServiceDeploymentSpec{Build: &deploymentv1.BuildSource{Type: buildSourceTypeImage}},
		}},
	}
	if err := protovalidate.Validate(req); err == nil {
		t.Fatal("a request naming a region twice passed validation")
	}
	req.Regions = []string{testRegion}
	if err := protovalidate.Validate(req); err != nil {
		t.Fatalf("a request naming each region once failed validation: %v", err)
	}
}
