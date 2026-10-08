package logstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var base = time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)

type fakeProxy struct {
	observabilityv1connect.UnimplementedObservabilityProxyServiceHandler

	mu       sync.Mutex
	entries  []*observabilityv1.LogEntry
	pageSize int
	tails    int
	queries  []*observabilityv1.QueryLogsRequest
	auth     []string
}

func entry(second int, body string) *observabilityv1.LogEntry {
	ts := base.Add(time.Duration(second) * time.Second)
	return &observabilityv1.LogEntry{Timestamp: timestamppb.New(ts), Body: body}
}

func (p *fakeProxy) QueryLogs(
	_ context.Context,
	req *connect.Request[observabilityv1.QueryLogsRequest],
) (*connect.Response[observabilityv1.QueryLogsResponse], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries = append(p.queries, req.Msg)
	p.auth = append(p.auth, req.Header().Get("Authorization"))
	entries := slices.Clone(p.entries)
	if req.Msg.GetOrder() == observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST {
		slices.Reverse(entries)
	}
	offset := 0
	if req.Msg.GetCursor() != "" {
		offset = p.pageSize
	}
	end := len(entries)
	next := ""
	if p.pageSize > 0 && offset == 0 && end > p.pageSize {
		end = p.pageSize
		next = "page-2"
	}
	return connect.NewResponse(&observabilityv1.QueryLogsResponse{Entries: entries[offset:end], NextCursor: next}), nil
}

func (p *fakeProxy) TailLogs(
	ctx context.Context,
	_ *connect.Request[observabilityv1.TailLogsRequest],
	stream *connect.ServerStream[observabilityv1.TailLogsResponse],
) error {
	p.mu.Lock()
	p.tails++
	first := p.tails == 1
	p.mu.Unlock()
	sent := []*observabilityv1.LogEntry{entry(1, "backfilled"), entry(5, "tailed")}
	if !first {
		sent = append(sent, entry(6, "after reconnect"))
	}
	for _, e := range sent {
		resp := &observabilityv1.TailLogsResponse{Event: &observabilityv1.TailLogsResponse_Entry{Entry: e}}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
	if first {
		return nil
	}
	<-ctx.Done()
	return nil
}

func serve(t *testing.T, proxy *fakeProxy) Proxy {
	t.Helper()
	return serveHandler(t, proxy)
}

func serveHandler(t *testing.T, proxy observabilityv1connect.ObservabilityProxyServiceHandler) Proxy {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(observabilityv1connect.NewObservabilityProxyServiceHandler(proxy))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Proxy{ClusterID: srv.URL, Region: "test", URL: srv.URL}
}

func newClient() *Client {
	return &Client{
		Token: "token",
		NewClient: func(url string) observabilityv1connect.ObservabilityProxyServiceClient {
			return observabilityv1connect.NewObservabilityProxyServiceClient(http.DefaultClient, url)
		},
	}
}

func collect(entries *[]string) Emit {
	return func(e *observabilityv1.LogEntry) error {
		*entries = append(*entries, e.GetBody())
		return nil
	}
}

func TestBackfillMergesRegionsOldestFirstAndKeepsTheLastLines(t *testing.T) {
	east := serve(t, &fakeProxy{entries: []*observabilityv1.LogEntry{entry(0, "east 0"), entry(2, "east 2")}})
	west := serve(t, &fakeProxy{entries: []*observabilityv1.LogEntry{entry(1, "west 1"), entry(3, "west 3")}})
	proxies := []Proxy{east, west}
	filter := Filter{WorkspaceID: "ws", ResourceIDs: []string{"r"}}

	var all []string
	if err := newClient().Backfill(t.Context(), proxies, filter, base, base.Add(time.Hour), 0, collect(&all)); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if want := []string{"east 0", "west 1", "east 2", "west 3"}; !slices.Equal(all, want) {
		t.Fatalf("entries = %v, want %v", all, want)
	}

	var last []string
	end := base.Add(time.Hour)
	if err := newClient().Backfill(t.Context(), proxies, filter, base, end, 2, collect(&last)); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if want := []string{"east 2", "west 3"}; !slices.Equal(last, want) {
		t.Fatalf("last two = %v, want %v", last, want)
	}
}

func TestBackfillFollowsCursorsAndClampsTheRange(t *testing.T) {
	proxy := &fakeProxy{
		entries:  []*observabilityv1.LogEntry{entry(0, "a"), entry(1, "b"), entry(2, "c")},
		pageSize: 2,
	}
	target := serve(t, proxy)
	filter := BuildFilter("ws", "build-1")
	end := base.Add(48 * time.Hour)

	var got []string
	if err := newClient().Backfill(t.Context(), []Proxy{target}, filter, base, end, 0, collect(&got)); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if len(proxy.queries) != 2 {
		t.Fatalf("%d queries, want 2 pages", len(proxy.queries))
	}
	first := proxy.queries[0]
	if span := first.GetEndTime().AsTime().Sub(first.GetStartTime().AsTime()); span != MaxRange {
		t.Fatalf("queried %s, want the range clamped to %s", span, MaxRange)
	}
	if first.GetLabels()["loco.io/build-id"] != "build-1" || first.GetWorkspaceId() != "ws" {
		t.Fatalf("query = %v, want the build's labels and workspace", first)
	}
	if proxy.auth[0] != "Bearer token" {
		t.Fatalf("authorization = %q", proxy.auth[0])
	}
}

func TestTailSkipsBackfilledEntriesAndReconnects(t *testing.T) {
	proxy := &fakeProxy{}
	target := serve(t, proxy)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var mu sync.Mutex
	var got []string
	emit := func(e *observabilityv1.LogEntry) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e.GetBody())
		if len(got) == 2 {
			cancel()
		}
		return nil
	}
	if err := newClient().Tail(ctx, []Proxy{target}, Filter{WorkspaceID: "ws"}, base.Add(time.Second), emit); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if want := []string{"tailed", "after reconnect"}; !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

const (
	proxyTailLookback = 2 * time.Second
	streamWait        = 5 * time.Second
)

type windowProxy struct {
	observabilityv1connect.UnimplementedObservabilityProxyServiceHandler

	now      time.Time
	backfill []*observabilityv1.LogEntry
	live     []*observabilityv1.LogEntry
}

func (p *windowProxy) QueryLogs(
	_ context.Context,
	_ *connect.Request[observabilityv1.QueryLogsRequest],
) (*connect.Response[observabilityv1.QueryLogsResponse], error) {
	return connect.NewResponse(&observabilityv1.QueryLogsResponse{Entries: p.backfill}), nil
}

func (p *windowProxy) TailLogs(
	ctx context.Context,
	req *connect.Request[observabilityv1.TailLogsRequest],
	stream *connect.ServerStream[observabilityv1.TailLogsResponse],
) error {
	after := p.now.Add(-proxyTailLookback)
	if since := req.Msg.GetSince(); since != nil {
		after = since.AsTime()
	}
	for _, e := range p.live {
		if !e.GetTimestamp().AsTime().After(after) {
			continue
		}
		resp := &observabilityv1.TailLogsResponse{Event: &observabilityv1.TailLogsResponse_Entry{Entry: e}}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}

func streamUntil(t *testing.T, proxy *windowProxy, window Window, want int) []*observabilityv1.LogEntry {
	t.Helper()
	target := serveHandler(t, proxy)
	ctx, cancel := context.WithTimeout(t.Context(), streamWait)
	defer cancel()
	var mu sync.Mutex
	var got []*observabilityv1.LogEntry
	emit := func(e *observabilityv1.LogEntry) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
		if len(got) == want {
			cancel()
		}
		return nil
	}
	if err := newClient().Stream(ctx, []Proxy{target}, Filter{WorkspaceID: "ws"}, window, emit); err != nil {
		t.Fatalf("stream: %v", err)
	}
	return got
}

func bodies(entries []*observabilityv1.LogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.GetBody())
	}
	return out
}

func TestFollowTailsFromTheEndOfTheBackfill(t *testing.T) {
	proxy := &windowProxy{
		now:      base.Add(6 * time.Second),
		backfill: []*observabilityv1.LogEntry{entry(1, "backfilled")},
		live:     []*observabilityv1.LogEntry{entry(1, "backfilled"), entry(3, "in the gap"), entry(5, "tailed")},
	}
	window := Window{Start: base, End: base.Add(time.Second), Follow: true}
	got := bodies(streamUntil(t, proxy, window, 3))
	if want := []string{"backfilled", "in the gap", "tailed"}; !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

func podEntry(second int, pod, body string) *observabilityv1.LogEntry {
	e := entry(second, body)
	e.ResourceAttributes = map[string]string{"k8s.pod.name": pod}
	return e
}

func TestTailKeepsTheSameLineFromTwoPods(t *testing.T) {
	first := podEntry(3, "app-a", "GET /health 200")
	second := podEntry(3, "app-b", "GET /health 200")
	proxy := &windowProxy{
		now:  base.Add(4 * time.Second),
		live: []*observabilityv1.LogEntry{first, second, first},
	}
	window := Window{Start: base, End: base.Add(time.Second), Follow: true}
	got := streamUntil(t, proxy, window, 2)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	pods := []string{got[0].GetResourceAttributes()["k8s.pod.name"], got[1].GetResourceAttributes()["k8s.pod.name"]}
	if want := []string{"app-a", "app-b"}; !slices.Equal(pods, want) {
		t.Fatalf("pods = %v, want %v", pods, want)
	}
}
