package interceptor

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
)

const streamReleaseWait = 5 * time.Second

func TestStreamLimitsRefuseStreamsButNotUnaryCalls(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	limiter, err := NewStreamLimiter(
		StreamLimits{PerReplica: 3, PerPrincipal: 2},
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := newHeldEventClient(t, limiter)
	cancelFirst := mustOpen(t, client, "alice")
	mustOpen(t, client, "alice")
	if _, err := openStream(t, client, "alice"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("third alice stream = %v, want resource_exhausted", err)
	}
	mustOpen(t, client, "bob")
	if _, err := openStream(t, client, "carol"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("fourth stream on the replica = %v, want resource_exhausted", err)
	}
	if _, err := client.ListEvents(callerContext(t.Context(), "carol"), &eventv1.ListEventsRequest{}); err != nil {
		t.Fatalf("unary call with every stream slot taken: %v", err)
	}
	if got, want := streamMetrics(t, reader), map[string]int64{
		"open": 3, "refused principal": 1, "refused replica": 1,
	}; !maps.Equal(got, want) {
		t.Fatalf("stream metrics = %v, want %v", got, want)
	}
	cancelFirst()
	deadline := time.Now().Add(streamReleaseWait)
	for {
		_, err := openStream(t, client, "carol")
		if err == nil {
			break
		}
		if connect.CodeOf(err) != connect.CodeResourceExhausted || time.Now().After(deadline) {
			t.Fatalf("stream after another closed = %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := streamMetrics(t, reader)["open"]; got != 3 {
		t.Fatalf("open streams after one closed and another opened = %d, want 3", got)
	}
}

func TestStreamLimitsAdmitExactlyTheLimitAtOnce(t *testing.T) {
	t.Parallel()
	limiter, err := NewStreamLimiter(
		StreamLimits{PerReplica: 100, PerPrincipal: 3},
		sdkmetric.NewMeterProvider().Meter("test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: uuid.New()}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if _, err := limiter.acquire(entity); err == nil {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if got := admitted.Load(); got != 3 {
		t.Fatalf("admitted = %d, want 3", got)
	}
	for range admitted.Load() {
		limiter.release(entity)
	}
	if limiter.open != 0 || len(limiter.byPrincipal) != 0 {
		t.Fatalf("after release: %d open, principals %v", limiter.open, limiter.byPrincipal)
	}
}

// heldEvents sends one batch on each event stream and holds it open until the client leaves.
type heldEvents struct {
	eventv1connect.UnimplementedEventServiceHandler
}

func (heldEvents) ListEvents(context.Context, *eventv1.ListEventsRequest) (*eventv1.ListEventsResponse, error) {
	return &eventv1.ListEventsResponse{}, nil
}

func (heldEvents) StreamEvents(
	ctx context.Context,
	_ *eventv1.StreamEventsRequest,
	stream eventv1connect.EventServiceStreamEventsServerStream,
) error {
	if err := stream.Send(&eventv1.StreamEventsResponse{}); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

// newHeldEventClient serves heldEvents behind limiter, as the principal its Authorization header names.
func newHeldEventClient(t *testing.T, limiter *streamLimiter) eventv1connect.EventServiceClient {
	t.Helper()
	var mu sync.Mutex
	entities := map[string]genDb.Entity{}
	principals := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			caller := info.RequestHeader().Get("Authorization")
			mu.Lock()
			entity, ok := entities[caller]
			if !ok {
				entity = genDb.Entity{Type: genDb.EntityTypeUser, ID: uuid.New()}
				entities[caller] = entity
			}
			mu.Unlock()
			return next(reqctx.WithPrincipal(ctx, reqctx.Principal{Entity: entity}), spec, stream)
		}
	}
	rpcServer := connect.NewServer(NewErrorInterceptor(), principals, limiter.Interceptor())
	eventv1connect.RegisterEventServiceHandler(rpcServer, heldEvents{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return eventv1connect.NewEventServiceClient(
		connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL)),
	)
}

func callerContext(ctx context.Context, caller string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", caller)
	return ctx
}

// openStream opens an event stream as caller and reads its first batch; the stream stays open
// until the returned cancel or the test's end.
func openStream(t *testing.T, client eventv1connect.EventServiceClient, caller string) (context.CancelFunc, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(callerContext(t.Context(), caller))
	t.Cleanup(cancel)
	stream, err := client.StreamEvents(ctx, &eventv1.StreamEventsRequest{})
	if err != nil {
		return cancel, err
	}
	t.Cleanup(func() {
		if closeErr := stream.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	_, err = stream.Receive()
	return cancel, err
}

func mustOpen(t *testing.T, client eventv1connect.EventServiceClient, caller string) context.CancelFunc {
	t.Helper()
	cancel, err := openStream(t, client, caller)
	if err != nil {
		t.Fatalf("%s stream: %v", caller, err)
	}
	return cancel
}

// streamMetrics sums the open streams and the refusals per limit reader has collected.
func streamMetrics(t *testing.T, reader sdkmetric.Reader) map[string]int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				switch m.Name {
				case "loco.api.streams.open":
					got["open"] += point.Value
				case "loco.api.streams.refused":
					limit, _ := point.Attributes.Value(streamLimitAttr)
					got["refused "+limit.AsString()] += point.Value
				}
			}
		}
	}
	return got
}
