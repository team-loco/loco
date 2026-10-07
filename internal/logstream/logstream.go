package logstream

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	MaxRange      = 24 * time.Hour
	pageSize      = 1000
	overlapWindow = 5 * time.Second
	retryBackoff  = 2 * time.Second
	labelBuildID  = "loco.io/build-id"

	entryKeyFields = 4
)

var ErrNoProxy = errors.New("no log proxy is registered for the cluster")

type Proxy struct {
	ClusterID string
	Region    string
	URL       string
}

type Filter struct {
	WorkspaceID string
	ResourceIDs []string
	Labels      map[string]string
}

func BuildFilter(workspaceID, buildID string) Filter {
	return Filter{WorkspaceID: workspaceID, Labels: map[string]string{labelBuildID: buildID}}
}

type Emit func(entry *observabilityv1.LogEntry) error

type Client struct {
	Token     string
	NewClient func(url string) observabilityv1connect.ObservabilityProxyServiceClient
}

func bearer(token string) string {
	return "Bearer " + token
}

func (c *Client) authorize(req connect.AnyRequest) {
	authorization := bearer(c.Token)
	req.Header().Set("Authorization", authorization)
}

func Proxies(
	ctx context.Context,
	access observabilityv1connect.ObservabilityAccessServiceClient,
	token string,
	workspaceID string,
	clusterID string,
) ([]Proxy, error) {
	req := connect.NewRequest(&observabilityv1.GetObservabilityAccessRequest{WorkspaceId: workspaceID})
	authorization := bearer(token)
	req.Header().Set("Authorization", authorization)
	resp, err := access.GetObservabilityAccess(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("look up log proxies: %w", err)
	}
	var proxies []Proxy
	for _, cluster := range resp.Msg.GetClusters() {
		id := cluster.GetClusterId()
		url := cluster.GetProxyUrl()
		if clusterID != "" && id != clusterID {
			continue
		}
		if url == "" {
			continue
		}
		region := cluster.GetRegion()
		proxies = append(proxies, Proxy{ClusterID: id, Region: region, URL: url})
	}
	if len(proxies) == 0 {
		return nil, ErrNoProxy
	}
	return proxies, nil
}

type Window struct {
	Start      time.Time
	End        time.Time
	Limit      int32
	Follow     bool
	Backfilled func()
}

func (c *Client) Stream(ctx context.Context, proxies []Proxy, filter Filter, window Window, emit Emit) error {
	since := window.Start
	tracked := func(entry *observabilityv1.LogEntry) error {
		if ts := entry.GetTimestamp().AsTime(); ts.After(since) {
			since = ts
		}
		return emit(entry)
	}
	err := c.Backfill(ctx, proxies, filter, window.Start, window.End, window.Limit, tracked)
	if window.Backfilled != nil {
		window.Backfilled()
	}
	if err != nil {
		return err
	}
	if !window.Follow {
		return nil
	}
	return c.Tail(ctx, proxies, filter, since, emit)
}

func (c *Client) Backfill(
	ctx context.Context,
	proxies []Proxy,
	filter Filter,
	start, end time.Time,
	limit int32,
	emit Emit,
) error {
	if end.Sub(start) > MaxRange {
		start = end.Add(-MaxRange)
	}
	var entries []*observabilityv1.LogEntry
	for _, proxy := range proxies {
		found, err := c.query(ctx, proxy, filter, start, end, limit)
		if err != nil {
			return err
		}
		entries = append(entries, found...)
	}
	slices.SortStableFunc(entries, compareEntries)
	if limit > 0 && len(entries) > int(limit) {
		entries = entries[len(entries)-int(limit):]
	}
	for _, entry := range entries {
		if err := emit(entry); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) query(
	ctx context.Context,
	proxy Proxy,
	filter Filter,
	start, end time.Time,
	limit int32,
) ([]*observabilityv1.LogEntry, error) {
	client := c.NewClient(proxy.URL)
	order := observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST
	size := int32(pageSize)
	if limit > 0 {
		order = observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST
		size = min(limit, pageSize)
	}
	startTime := timestamppb.New(start)
	endTime := timestamppb.New(end)
	var entries []*observabilityv1.LogEntry
	cursor := ""
	for {
		req := connect.NewRequest(&observabilityv1.QueryLogsRequest{
			WorkspaceId: filter.WorkspaceID,
			ResourceIds: filter.ResourceIDs,
			Labels:      filter.Labels,
			StartTime:   startTime,
			EndTime:     endTime,
			Limit:       size,
			Cursor:      cursor,
			Order:       order,
		})
		c.authorize(req)
		resp, err := client.QueryLogs(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("query logs in %s: %w", proxy.Region, err)
		}
		page := resp.Msg.GetEntries()
		entries = append(entries, page...)
		cursor = resp.Msg.GetNextCursor()
		if cursor == "" || (limit > 0 && len(entries) >= int(limit)) {
			return entries, nil
		}
	}
}

func (c *Client) Tail(ctx context.Context, proxies []Proxy, filter Filter, since time.Time, emit Emit) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	locked := func(entry *observabilityv1.LogEntry) error {
		mu.Lock()
		defer mu.Unlock()
		return emit(entry)
	}
	errs := make(chan error, len(proxies))
	var wg sync.WaitGroup
	for _, proxy := range proxies {
		wg.Go(func() {
			errs <- c.tailProxy(ctx, proxy, filter, since, locked)
		})
	}
	var first error
	for range proxies {
		err := <-errs
		if err != nil && first == nil {
			first = err
			cancel()
		}
	}
	wg.Wait()
	if stopped(ctx) && first == nil {
		return nil
	}
	return first
}

func (c *Client) tailProxy(ctx context.Context, proxy Proxy, filter Filter, since time.Time, emit Emit) error {
	client := c.NewClient(proxy.URL)
	seen := newOverlap(since)
	for {
		start := timestamppb.New(seen.latest)
		req := connect.NewRequest(&observabilityv1.TailLogsRequest{
			WorkspaceId: filter.WorkspaceID,
			ResourceIds: filter.ResourceIDs,
			Labels:      filter.Labels,
			Since:       start,
		})
		c.authorize(req)
		stream, err := client.TailLogs(ctx, req)
		if err != nil {
			return tailError(ctx, proxy, err)
		}
		for stream.Receive() {
			entry := stream.Msg().GetEntry()
			if entry == nil || !seen.fresh(entry) {
				continue
			}
			if emitErr := emit(entry); emitErr != nil {
				closeErr := stream.Close()
				return errors.Join(emitErr, closeErr)
			}
		}
		streamErr := stream.Err()
		closeErr := stream.Close()
		if stopped(ctx) {
			return nil
		}
		if streamErr != nil {
			joined := errors.Join(streamErr, closeErr)
			return tailError(ctx, proxy, joined)
		}
		timer := time.NewTimer(retryBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func tailError(ctx context.Context, proxy Proxy, err error) error {
	if stopped(ctx) {
		return nil
	}
	return fmt.Errorf("tail logs in %s: %w", proxy.Region, err)
}

type overlap struct {
	after  time.Time
	latest time.Time
	keys   map[string]time.Time
}

func newOverlap(since time.Time) *overlap {
	return &overlap{after: since, latest: since, keys: map[string]time.Time{}}
}

func entryKey(entry *observabilityv1.LogEntry) string {
	ts := entry.GetTimestamp().AsTime()
	attributes := entry.GetResourceAttributes()
	stamp := ts.Format(time.RFC3339Nano)
	parts := make([]string, 0, len(attributes)+entryKeyFields)
	parts = append(parts, stamp, entry.GetResourceId(), entry.GetSeverity())
	for _, name := range slices.Sorted(maps.Keys(attributes)) {
		parts = append(parts, name+"="+attributes[name])
	}
	parts = append(parts, entry.GetBody())
	return strings.Join(parts, "\x00")
}

func (o *overlap) fresh(entry *observabilityv1.LogEntry) bool {
	ts := entry.GetTimestamp().AsTime()
	if !ts.After(o.after) {
		return false
	}
	key := entryKey(entry)
	if _, ok := o.keys[key]; ok {
		return false
	}
	o.keys[key] = ts
	if ts.After(o.latest) {
		o.latest = ts
	}
	horizon := ts.Add(-overlapWindow)
	for k, seenAt := range o.keys {
		if seenAt.Before(horizon) {
			delete(o.keys, k)
		}
	}
	return true
}

func compareEntries(a, b *observabilityv1.LogEntry) int {
	at := a.GetTimestamp().AsTime()
	bt := b.GetTimestamp().AsTime()
	return at.Compare(bt)
}

func stopped(ctx context.Context) bool {
	return ctx.Err() != nil
}
