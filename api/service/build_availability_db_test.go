package service

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

func (f *buildFixture) disableBuilds(t *testing.T, clusterID uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(
		f.ctx,
		`UPDATE clusters SET builds_enabled = false WHERE id = $1`,
		clusterID,
	); err != nil {
		t.Fatalf("disable builds on %s: %v", clusterID, err)
	}
}

func wantBuildsUnavailable(t *testing.T, err error, wantMessage string) {
	t.Helper()
	wantCode(t, err, connect.CodeFailedPrecondition)
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("error %v is not a connect error", err)
	}
	if connectErr.Message() != wantMessage {
		t.Fatalf("message = %q, want %q", connectErr.Message(), wantMessage)
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if _, isUnavailable := value.(*buildv1.BuildsUnavailable); isUnavailable {
			return
		}
	}
	t.Fatalf("error %v carries no BuildsUnavailable detail", err)
}

func TestStartBuildSkipsClustersWithoutBuilds(t *testing.T) {
	f := newBuildFixture(t)
	if _, err := f.pool.Exec(
		f.ctx,
		`UPDATE clusters SET created_at = NOW() - INTERVAL '2 hours' WHERE id = $1`,
		f.otherCluster,
	); err != nil {
		t.Fatalf("make the other cluster the oldest: %v", err)
	}
	f.disableBuilds(t, f.otherCluster)

	id := f.create(t, 10).GetBuildId()
	f.uploadFor(t, id, 10)
	build, err := f.start(id)
	if err != nil {
		t.Fatalf("start build: %v", err)
	}
	if got, want := build.GetClusterId(), f.clusterID.String(); got != want {
		t.Fatalf("build queued on %q, want %q, the oldest cluster that accepts builds", got, want)
	}

	id = f.create(t, 10).GetBuildId()
	f.uploadFor(t, id, 10)
	f.disableBuilds(t, f.clusterID)
	_, err = f.start(id)
	wantBuildsUnavailable(t, err, errNoBuildCluster.Error())
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_AWAITING_UPLOAD)
}

func TestCreateBuildFailsBeforeUploadWhenNoClusterAcceptsBuilds(t *testing.T) {
	f := newBuildFixture(t)
	f.disableBuilds(t, f.clusterID)
	f.disableBuilds(t, f.otherCluster)

	req := connect.NewRequest(&buildv1.CreateBuildRequest{
		ResourceId:     f.resourceID.String(),
		DockerfilePath: testDockerfile,
		SourceSize:     10,
	})
	_, err := f.server.CreateBuild(f.ctx, req)
	wantBuildsUnavailable(t, err, errNoBuildCluster.Error())
	if len(f.bucket.presigned) != 0 {
		t.Fatalf("presigned %d uploads for a build no cluster can run", len(f.bucket.presigned))
	}
}

func TestBuildServiceWithoutABucketIsUnavailable(t *testing.T) {
	f := newBuildFixture(t)
	server := NewBuildServer(f.pool, f.queries, nil, f.server.config)
	req := connect.NewRequest(&buildv1.CreateBuildRequest{
		ResourceId:     f.resourceID.String(),
		DockerfilePath: testDockerfile,
		SourceSize:     10,
	})
	_, err := server.CreateBuild(f.ctx, req)
	wantBuildsUnavailable(t, err, ErrBuildsNotEnabled.Error())
}

func TestBuildsQueuedOnAClusterThatDisabledBuildsFail(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()
	id := f.queued(t)
	f.disableBuilds(t, f.clusterID)

	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	if starts := startedBuilds(sent.take()); len(starts) != 0 {
		t.Fatalf("sent %d StartBuild messages to a cluster without builds", len(starts))
	}
	build := f.get(t, id)
	wantStatus(t, build, buildv1.BuildStatus_BUILD_STATUS_FAILED)
	if build.GetMessage() != buildsDisabledMessage {
		t.Fatalf("message = %q, want %q", build.GetMessage(), buildsDisabledMessage)
	}
	if !f.bucket.wasDeleted(sourceKeyFor(id)) {
		t.Fatal("the failed build's source was not deleted")
	}
}

func TestRegisterAndHeartbeatRecordWhetherTheClusterAcceptsBuilds(t *testing.T) {
	f := newBuildFixture(t)
	server := NewAgentServer(f.pool, f.queries, nil, f.bucket)
	register := func(enabled bool) {
		t.Helper()
		req := connect.NewRequest(&agentv1.RegisterRequest{AgentVersion: "test", BuildsEnabled: enabled})
		req.Header().Set("Authorization", "Bearer "+testAgentToken)
		if _, err := server.Register(f.ctx, req); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	buildsEnabled := func() bool {
		t.Helper()
		enabled, err := f.queries.GetClusterBuildsEnabled(f.ctx, f.clusterID)
		if err != nil {
			t.Fatalf("read builds_enabled: %v", err)
		}
		return enabled
	}

	register(false)
	if buildsEnabled() {
		t.Fatal("builds_enabled stayed true after the agent registered without builds")
	}
	register(true)
	if !buildsEnabled() {
		t.Fatal("builds_enabled is false after the agent registered with builds")
	}

	client := startSyncServerWithSources(t, f.deployFixture, f.bucket)
	stream := client.Heartbeat(f.ctx)
	stream.RequestHeader().Set("Authorization", "Bearer "+testAgentToken)
	clusterID := f.clusterID.String()
	if err := stream.Send(&agentv1.HeartbeatRequest{ClusterId: clusterID, BuildsEnabled: false}); err != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for buildsEnabled() {
		if time.Now().After(deadline) {
			t.Fatal("builds_enabled stayed true after a heartbeat without builds")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := stream.CloseRequest(); err != nil {
		t.Fatalf("close heartbeat stream: %v", err)
	}
}
