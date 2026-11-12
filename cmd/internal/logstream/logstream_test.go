package logstream

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/rpc"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
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
	ctx context.Context,
	req *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries = append(p.queries, req)
	info, _ := connect.CallInfoForServerContext(ctx)
	p.auth = append(p.auth, info.RequestHeader().Get("Authorization"))
	entries := slices.Clone(p.entries)
	if req.GetOrder() == observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST {
		slices.Reverse(entries)
	}
	offset := 0
	if req.GetCursor() != "" {
		var err error
		offset, err = strconv.Atoi(req.GetCursor())
		if err != nil {
			return nil, err
		}
	}
	size := p.pageSize
	if size == 0 {
		size = len(entries)
	}
	if req.GetLimit() > 0 {
		size = min(size, int(req.GetLimit()))
	}
	end := min(offset+size, len(entries))
	next := ""
	if end < len(entries) {
		next = strconv.Itoa(end)
	}

	return &observabilityv1.QueryLogsResponse{Entries: entries[offset:end], NextCursor: next}, nil
}

func (p *fakeProxy) TailLogs(
	ctx context.Context,
	_ *observabilityv1.TailLogsRequest,
	stream observabilityv1connect.ObservabilityProxyServiceTailLogsServerStream,
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

func newClient(proxy observabilityv1connect.ObservabilityProxyServiceHandler) *Client {
	server := connect.NewServer()
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(server, proxy)
	transport := connectinprocess.New(server)
	client := connect.NewClient(transport, rpc.BearerInterceptor("token"))
	return &Client{Proxy: observabilityv1connect.NewObservabilityProxyServiceClient(client)}
}

func collect(entries *[]string) Emit {
	return func(e *observabilityv1.LogEntry) error {
		*entries = append(*entries, e.GetBody())
		return nil
	}
}

func TestBackfillQueriesOneRegionalBackendAndKeepsTheLastLines(t *testing.T) {
	proxy := &fakeProxy{
		entries: []*observabilityv1.LogEntry{
			entry(0, "east 0"),
			entry(1, "west 1"),
			entry(2, "east 2"),
			entry(3, "west 3"),
		},
	}
	client := newClient(proxy)
	filter := Filter{WorkspaceID: "ws", ResourceIDs: []string{"r"}}
	var all []string
	if err := client.Backfill(t.Context(), filter, base, base.Add(time.Hour), 0, collect(&all)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(all, []string{"east 0", "west 1", "east 2", "west 3"}) {
		t.Fatalf("entries = %v", all)
	}
	if len(proxy.queries) != 1 {
		t.Fatalf("queries = %d, want one regional request", len(proxy.queries))
	}
	var last []string
	if err := client.Backfill(t.Context(), filter, base, base.Add(time.Hour), 2, collect(&last)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(last, []string{"east 2", "west 3"}) {
		t.Fatalf("last = %v", last)
	}
}

func TestBackfillFollowsCursorsAndClampsTheRange(t *testing.T) {
	proxy := &fakeProxy{
		entries:  []*observabilityv1.LogEntry{entry(0, "a"), entry(1, "b"), entry(2, "c")},
		pageSize: 2,
	}
	client := newClient(proxy)
	filter := BuildFilter("ws", "build-1")
	end := base.Add(48 * time.Hour)

	var got []string
	if err := client.Backfill(t.Context(), filter, base, end, 0, collect(&got)); err != nil {
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
	client := newClient(proxy)
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
	if err := client.Tail(ctx, Filter{WorkspaceID: "ws"}, base.Add(time.Second), emit); err != nil {
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
	_ *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	return &observabilityv1.QueryLogsResponse{Entries: p.backfill}, nil
}

func (p *windowProxy) TailLogs(
	ctx context.Context,
	req *observabilityv1.TailLogsRequest,
	stream observabilityv1connect.ObservabilityProxyServiceTailLogsServerStream,
) error {
	after := p.now.Add(-proxyTailLookback)
	if since := req.GetSince(); since != nil {
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
	client := newClient(proxy)
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
	if err := client.Stream(ctx, Filter{WorkspaceID: "ws"}, window, emit); err != nil {
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

func TestBackfillUsesServerPagesAndHonorsLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int32
		sizes []int32
		want  []string
	}{
		{"all", 0, []int32{0, 0, 0}, []string{"a", "b", "c", "d", "e"}},
		{"limited", 3, []int32{0, 0}, []string{"c", "d", "e"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := &fakeProxy{
				entries: []*observabilityv1.LogEntry{
					entry(0, "a"), entry(1, "b"), entry(2, "c"),
					entry(3, "d"), entry(4, "e"),
				},
				pageSize: 2,
			}
			var got []string
			if err := newClient(
				proxy,
			).Backfill(t.Context(), Filter{WorkspaceID: "ws"}, base, base.Add(time.Hour), tc.limit, collect(&got)); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("entries = %v, want %v", got, tc.want)
			}
			sizes := make([]int32, 0, len(proxy.queries))
			for _, req := range proxy.queries {
				sizes = append(sizes, req.GetLimit())
			}
			if !slices.Equal(sizes, tc.sizes) {
				t.Fatalf("page sizes = %v, want %v", sizes, tc.sizes)
			}
		})
	}
}
