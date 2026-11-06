package installation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const verificationLogsTestTimeout = 10 * time.Second
const verificationLogsTestMarker = "verification-log-marker"

type verificationLogsAPI struct {
	observabilityv1connect.UnimplementedObservabilityProxyServiceHandler
	t     *testing.T
	start time.Time
	ends  []time.Time
}

func (api *verificationLogsAPI) QueryLogs(
	_ context.Context,
	request *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	if err := protovalidate.Validate(request); err != nil {
		api.t.Logf("query validation failed: %v", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, "invalid verification log query").WithCause(err)
	}
	if !request.GetStartTime().AsTime().Equal(api.start) {
		api.t.Error("log query lost the pre-deployment start time")
	}
	api.ends = append(api.ends, request.GetEndTime().AsTime())
	if len(api.ends) == 1 {
		return &observabilityv1.QueryLogsResponse{}, nil
	}
	return &observabilityv1.QueryLogsResponse{
		Entries: []*observabilityv1.LogEntry{{Body: verificationLogsTestMarker}},
	}, nil
}

func TestVerificationLogsUseValidAdvancingTimeBounds(t *testing.T) {
	started := time.Now()
	api := &verificationLogsAPI{t: t, start: started}
	server := connect.NewServer()
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(server, api)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	defer endpoint.Close()
	clients := rpc.ForHost(endpoint.URL, "fixture-token")
	request := &observabilityv1.QueryLogsRequest{
		WorkspaceId: uuid.NewString(), EnvironmentId: uuid.NewString(), ResourceIds: []string{uuid.NewString()},
		StartTime: timestamppb.New(started), Search: verificationLogsTestMarker,
	}
	ctx, cancel := context.WithTimeout(t.Context(), verificationLogsTestTimeout)
	defer cancel()
	if err := verificationLogs(ctx, clients, request, verificationLogsTestMarker); err != nil {
		t.Fatalf("verification logs rejected the query: %v", err)
	}
	if len(api.ends) != 2 || !api.ends[1].After(api.ends[0]) {
		t.Fatal("log polling did not advance the query end time")
	}
	request.ResourceIds = []string{"not-a-uuid"}
	if err := verificationLogs(ctx, clients, request, verificationLogsTestMarker); !errors.Is(err, errVerification) {
		t.Fatalf("log polling bypassed resource ID validation: %v", err)
	}
}
