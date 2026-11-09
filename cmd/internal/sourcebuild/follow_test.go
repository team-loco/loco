package sourcebuild

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const followTestMargin = 3 * buildPollInterval

type blockedBackfill struct {
	buildv1connect.UnimplementedBuildServiceHandler
	observabilityv1connect.UnimplementedObservabilityProxyServiceHandler
	started  chan struct{}
	canceled chan struct{}
	terminal chan time.Time
	build    *buildv1.Build
}

func (s *blockedBackfill) GetBuild(ctx context.Context, _ *buildv1.GetBuildRequest) (*buildv1.GetBuildResponse, error) {
	select {
	case <-s.started:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.terminal <- time.Now()
	return &buildv1.GetBuildResponse{Build: s.build}, nil
}

func (s *blockedBackfill) QueryLogs(ctx context.Context, _ *observabilityv1.QueryLogsRequest) (
	*observabilityv1.QueryLogsResponse, error,
) {
	close(s.started)
	<-ctx.Done()
	close(s.canceled)
	return nil, ctx.Err()
}

func TestFollowBoundsBlockedBackfillAfterBuildFinishes(t *testing.T) {
	for _, status := range []buildv1.BuildStatus{
		buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED,
		buildv1.BuildStatus_BUILD_STATUS_FAILED,
		buildv1.BuildStatus_BUILD_STATUS_CANCELED,
	} {
		name := StatusLabel(status)
		t.Run(name, func(t *testing.T) {
			buildID, clusterID := "build-drain", "cluster-drain"
			createdAt := timestamppb.Now()
			build := &buildv1.Build{Id: buildID, ClusterId: new(clusterID), Status: status,
				CreatedAt: createdAt}
			service := &blockedBackfill{started: make(chan struct{}), canceled: make(chan struct{}),
				terminal: make(chan time.Time, 1), build: build}
			server := connect.NewServer()
			buildv1connect.RegisterBuildServiceHandler(server, service)
			observabilityv1connect.RegisterObservabilityProxyServiceHandler(server, service)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			endpoint := httptest.NewServer(mux)
			defer endpoint.Close()
			clients := rpc.ForHost(endpoint.URL, "")
			follower := NewFollower(clients, io.Discard)
			parent := t.Context()
			ctx, cancel := context.WithTimeout(parent, buildLogDrain+followTestMargin)
			defer cancel()
			initial := &buildv1.Build{Id: buildID, ClusterId: new(clusterID),
				Status: buildv1.BuildStatus_BUILD_STATUS_RUNNING, CreatedAt: createdAt}
			finished, err := follower.Follow(ctx, initial, "workspace-drain")
			if err != nil {
				t.Fatal(err)
			}
			actual := finished.GetStatus()
			if actual != status {
				t.Fatalf("status = %v, want %v", actual, status)
			}
			elapsed := time.Since(<-service.terminal)
			if elapsed >= buildLogDrain+buildPollInterval {
				t.Errorf("blocked backfill delayed terminal build by %s, drain budget %s", elapsed, buildLogDrain)
			}
			select {
			case <-service.canceled:
			case <-time.After(buildPollInterval):
				t.Fatal("log backfill request was not canceled")
			}
		})
	}
}
