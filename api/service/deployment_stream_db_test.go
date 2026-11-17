package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/pgnotify"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/internal/rpc"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testWatchDetail  = "container exited with code 23"
	testWatchReplica = "replica-current"
)

func (f *deployFixture) watchClient(t *testing.T) deploymentv1connect.DeploymentServiceClient {
	t.Helper()
	server, principalCtx := f.deploymentWriter(t)
	ctx, cancel := context.WithCancel(t.Context())
	notifier := pgnotify.New(f.pool.Config().ConnConfig, "deployment_activity", 30*time.Second)
	if err := notifier.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); notifier.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	server.notifier = notifier
	principal, _ := reqctx.PrincipalFrom(principalCtx)
	principal.Scopes = []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
	}
	middleware := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(reqctx.WithPrincipal(ctx, principal), spec, stream)
		}
	}
	rpcServer := connect.NewServer(middleware)
	deploymentv1connect.RegisterDeploymentServiceHandler(rpcServer, server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	return rpc.ForHost(endpoint.URL, "").Deployment
}

func TestDeploymentStreamHistoryResumeAndNotifications(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.MustParse(created.GetDeploymentId())
	client := f.watchClient(t)
	for seq := int64(1); seq <= 405; seq++ {
		err = f.queries.InsertDeploymentActivity(
			t.Context(),
			genDb.InsertDeploymentActivityParams{
				DeploymentID: id,
				Seq:          seq,
				Revision:     1,
				At:           time.Now(),
				Severity:     activityInfo,
				Message:      "first observation",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := client.WatchDeployment(
		ctx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: id.String(), AfterSeq: 5},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for batch := range 2 {
		event, recvErr := stream.Receive()
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		if event.GetHasMoreActivity() != (batch == 0) {
			t.Fatal("incorrect continuation flag")
		}
		if len(event.GetActivity()) != 200 {
			t.Fatalf("batch length=%d", len(event.GetActivity()))
		}
		if event.GetActivity()[0].GetSeq() != int64(6+batch*200) ||
			event.GetActivity()[199].GetSeq() != int64(205+batch*200) {
			t.Fatal("cursor skipped or repeated entries")
		}
	}
	started := time.Now()
	if _, err = f.pool.Exec(
		t.Context(),
		`UPDATE deployments SET status='failed' WHERE id=$1`,
		id,
	); err != nil {
		t.Fatal(err)
	}
	event, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("notification took over one second")
	}
	if event.GetDeployment().GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED ||
		len(event.GetActivity()) != 0 {
		t.Fatalf("failed report=%v", event)
	}
	if _, err = f.pool.Exec(t.Context(), `UPDATE deployments SET status='running' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	event, err = stream.Receive()
	if err != nil || event.GetDeployment().GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
		t.Fatalf("failed stream closed: %v %v", event, err)
	}
	resumed, err := client.WatchDeployment(
		ctx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: id.String(), AfterSeq: 405},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	event, err = resumed.Receive()
	if err != nil || len(event.GetActivity()) != 0 {
		t.Fatalf("replay duplicates: %v %v", event, err)
	}
	page, err := client.ListDeploymentActivity(
		ctx,
		&deploymentv1.ListDeploymentActivityRequest{DeploymentId: id.String(), AfterSeq: 400, PageSize: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.GetActivity()) != 3 || page.GetNextSeq() != 403 {
		t.Fatalf("page=%v", page)
	}
	page, err = client.ListDeploymentActivity(
		ctx,
		&deploymentv1.ListDeploymentActivityRequest{
			DeploymentId: id.String(),
			AfterSeq:     page.GetNextSeq(),
			PageSize:     3,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.GetActivity()) != 2 || page.GetActivity()[0].GetSeq() != 404 {
		t.Fatalf("second page=%v", page)
	}
}

func TestDeploymentStreamTerminalStatuses(t *testing.T) {
	for _, status := range []string{"canceled", "superseded"} {
		t.Run(status, func(t *testing.T) {
			f := newDeployFixture(t)
			created, err := createDeployment(t, f, sameRegionSpec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(
				t.Context(),
				`UPDATE deployments SET status=$2 WHERE id=$1`,
				uuid.MustParse(created.GetDeploymentId()),
				status,
			); err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(
				t.Context(),
				`INSERT INTO deployment_activity(deployment_id,seq,revision,at,severity,message)
 SELECT $1,seq,1,NOW(),'info','Observed' FROM generate_series(1,201) seq`,
				uuid.MustParse(created.GetDeploymentId()),
			); err != nil {
				t.Fatal(err)
			}
			client := f.watchClient(t)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream, err := client.WatchDeployment(
				ctx,
				&deploymentv1.WatchDeploymentRequest{DeploymentId: created.GetDeploymentId()},
			)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			first, err := stream.Receive()
			if err != nil {
				t.Fatal(err)
			}
			if len(first.GetActivity()) != 200 || !first.GetHasMoreActivity() {
				t.Fatalf("first terminal batch=%v", first)
			}
			last, err := stream.Receive()
			if err != nil {
				t.Fatal(err)
			}
			if len(last.GetActivity()) != 1 || last.GetHasMoreActivity() || last.GetActivity()[0].GetSeq() != 201 {
				t.Fatalf("last terminal batch=%v", last)
			}

			if _, err = stream.Receive(); !errors.Is(err, io.EOF) {
				t.Fatalf("terminal receive=%v", err)
			}
		})
	}
}

func TestDeploymentStreamSnapshotAndActivityWake(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	client := f.watchClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := client.WatchDeployment(
		ctx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: created.GetDeploymentId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	initial, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if initial.GetDeployment().GetCurrentRevision() != 1 || len(initial.GetActivity()) != 0 {
		t.Fatalf("initial=%v", initial)
	}
	placement := f.placement(t, f.clusterID)
	since := timestamppb.New(time.Now().Add(-time.Minute))
	deadline := timestamppb.New(time.Now().Add(time.Minute))
	report := &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		DesiredReplicas:  1,
		Replicas: []*agentv1.ReplicaReport{
			{
				Uid:               "new-uid",
				Name:              testWatchReplica,
				Node:              "node-a",
				PlacementRevision: placement.DesiredRevision,
				State:             "Starting",
				StateSince:        since,
				Restarts:          2,
				Detail:            "container creating",
			},
			{
				Uid:               "old-uid",
				Name:              "old",
				Node:              "node-b",
				PlacementRevision: placement.DesiredRevision - 1,
				State:             "Stopping",
				StateSince:        since,
				StateDeadline:     deadline,
				ExitCode:          new(int32(0)),
				Detail:            "draining",
			},
		},
	}
	started := time.Now()
	f.agentServer().recordStatus(ctx, f.clusterID, 0, report)
	event, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("activity notification took over one second")
	}
	if len(event.GetActivity()) != 3 || len(event.GetDeployment().GetReplicas()) != 2 {
		t.Fatalf("activity snapshot=%v", event)
	}
	newReplica, oldReplica := event.GetDeployment().GetReplicas()[0], event.GetDeployment().GetReplicas()[1]
	if newReplica.GetPlacementRevision() != event.GetDeployment().GetCurrentRevision() ||
		oldReplica.GetPlacementRevision() == event.GetDeployment().GetCurrentRevision() ||
		!proto.Equal(newReplica.GetStateSince(), since) ||
		!proto.Equal(oldReplica.GetStateDeadline(), deadline) ||
		oldReplica.ExitCode == nil {
		t.Fatalf("replica conversion=%v", event.GetDeployment().GetReplicas())
	}
	get, err := client.GetDeployment(ctx, &deploymentv1.GetDeploymentRequest{DeploymentId: created.GetDeploymentId()})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(get.GetDeployment(), event.GetDeployment()) {
		t.Fatalf("get differs: %v", get)
	}
	list, err := client.ListDeployments(ctx, &deploymentv1.ListDeploymentsRequest{ResourceId: f.resourceID.String()})
	if err != nil {
		t.Fatal(err)
	}
	withoutReplicas := proto.CloneOf(event.GetDeployment())
	withoutReplicas.Replicas = nil
	if len(list.GetDeployments()) != 1 || !proto.Equal(list.GetDeployments()[0], withoutReplicas) {
		t.Fatalf("list differs: %v", list)
	}
	report.Problem = &agentv1.ProblemReport{
		Reason:      string(loco.ProblemCrashLoop),
		ReplicaName: testWatchReplica,
		Detail:      testWatchDetail,
		Since:       since,
	}
	f.agentServer().recordStatus(ctx, f.clusterID, 0, report)
	event, err = stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if event.GetDeployment().GetProblem().GetMessage() == "" ||
		event.GetDeployment().GetProblem().GetDetail() != testWatchDetail ||
		!proto.Equal(event.GetDeployment().GetProblem().GetSince(), since) {
		t.Fatalf("problem=%v", event.GetDeployment().GetProblem())
	}
}

func TestDeploymentProblemComesFromLatestSnapshot(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	client := f.watchClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := client.WatchDeployment(
		ctx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: created.GetDeploymentId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		DesiredReplicas:  1,
		Problem: &agentv1.ProblemReport{
			Reason:      string(loco.ProblemCrashLoop),
			Detail:      testWatchDetail,
			ReplicaName: testWatchReplica,
			Since:       timestamppb.Now(),
		},
	}
	server := f.agentServer()
	server.recordStatus(ctx, f.clusterID, 0, report)
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	report.Problem.Detail = "container exited with code 42"
	started := time.Now()
	server.recordStatus(ctx, f.clusterID, 0, report)
	event, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("snapshot-only problem change did not wake within one second")
	}
	if len(event.GetActivity()) != 0 ||
		event.GetDeployment().GetProblem().GetDetail() != report.GetProblem().GetDetail() {
		t.Fatalf("problem-only update=%v", event)
	}
	get, err := client.GetDeployment(ctx, &deploymentv1.GetDeploymentRequest{DeploymentId: created.GetDeploymentId()})
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.ListDeployments(ctx, &deploymentv1.ListDeploymentsRequest{ResourceId: f.resourceID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GetDeployments()) != 1 ||
		!proto.Equal(get.GetDeployment().GetProblem(), event.GetDeployment().GetProblem()) ||
		!proto.Equal(list.GetDeployments()[0].GetProblem(), event.GetDeployment().GetProblem()) {
		t.Fatal("get/list problem differs from watch")
	}
	if _, err = f.queries.RestartResourcePlacements(
		ctx,
		genDb.RestartResourcePlacementsParams{ResourceID: f.resourceID, EnvironmentID: f.envID},
	); err != nil {
		t.Fatal(err)
	}
	report.ObservedRevision++
	report.Problem = nil
	server.recordStatus(ctx, f.clusterID, 0, report)
	for {
		event, err = stream.Receive()
		if err != nil {
			t.Fatal(err)
		}
		if event.GetDeployment().GetCurrentRevision() == report.GetObservedRevision() &&
			event.GetDeployment().GetProblem() == nil {
			break
		}
	}

	get, err = client.GetDeployment(ctx, &deploymentv1.GetDeploymentRequest{DeploymentId: created.GetDeploymentId()})
	if err != nil {
		t.Fatal(err)
	}
	list, err = client.ListDeployments(ctx, &deploymentv1.ListDeploymentsRequest{ResourceId: f.resourceID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if get.GetDeployment().GetProblem() != nil || list.GetDeployments()[0].GetProblem() != nil {
		t.Fatal("cleared problem remains in get/list")
	}
}
