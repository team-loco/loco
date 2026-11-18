package sourcebuild

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

type blockedBackfill struct {
	buildv1connect.UnimplementedBuildServiceHandler
	observabilityv1connect.UnimplementedObservabilityServiceHandler
	started  chan struct{}
	canceled chan struct{}
	build    *buildv1.Build
}

func (s *blockedBackfill) GetBuild(ctx context.Context, _ *buildv1.GetBuildRequest) (*buildv1.GetBuildResponse, error) {
	select {
	case <-s.started:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
				build: build}
			server := connect.NewServer()
			buildv1connect.RegisterBuildServiceHandler(server, service)
			observabilityv1connect.RegisterObservabilityServiceHandler(server, service)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			endpoint := httptest.NewServer(mux)
			defer endpoint.Close()
			clients := rpc.ForHost(endpoint.URL, "")
			follower := NewFollower(clients, io.Discard)
			initial := &buildv1.Build{Id: buildID, ClusterId: new(clusterID),
				Status: buildv1.BuildStatus_BUILD_STATUS_RUNNING, CreatedAt: createdAt}
			// The backfill never ends, so Follow returning at all proves the drain budget ended it.
			finished, err := follower.Follow(t.Context(), initial, "workspace-drain")
			if err != nil {
				t.Fatal(err)
			}
			actual := finished.GetStatus()
			if actual != status {
				t.Fatalf("status = %v, want %v", actual, status)
			}
			<-service.canceled
		})
	}
}
