package logstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	MaxRange      = 24 * time.Hour
	pageSize      = 1000
	overlapWindow = 5 * time.Second
	retryBackoff  = 2 * time.Second
	// keep in sync with LabelBuildID in k8sapi/labels/labels.go
	labelBuildID = "loco.io/build-id"

	entryKeyFields = 4
)

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
	URL       string
	Token     string
	NewClient func(url, token string) observabilityv1connect.ObservabilityProxyServiceClient
}

type Window struct {
	Start      time.Time
	End        time.Time
	Limit      int32
	Follow     bool
	Backfilled func()
}

func (c *Client) Stream(ctx context.Context, filter Filter, window Window, emit Emit) error {
	since := window.Start
	tracked := func(entry *observabilityv1.LogEntry) error {
		if ts := entry.GetTimestamp().AsTime(); ts.After(since) {
			since = ts
		}
		return emit(entry)
	}
	err := c.Backfill(ctx, filter, window.Start, window.End, window.Limit, tracked)
	if window.Backfilled != nil {
		window.Backfilled()
	}
	if err != nil {
		return err
	}
	if !window.Follow {
		return nil
	}
	return c.Tail(ctx, filter, since, emit)
}

func (c *Client) Backfill(
	ctx context.Context,
	filter Filter,
	start, end time.Time,
	limit int32,
	emit Emit,
) error {
	if end.Sub(start) > MaxRange {
		start = end.Add(-MaxRange)
	}
	entries, err := c.query(ctx, filter, start, end, limit)
	if err != nil {
		return err
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
	filter Filter,
	start, end time.Time,
	limit int32,
) ([]*observabilityv1.LogEntry, error) {
	client := c.NewClient(c.URL, c.Token)
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
		req := &observabilityv1.QueryLogsRequest{
			WorkspaceId: filter.WorkspaceID,
			ResourceIds: filter.ResourceIDs,
			Labels:      filter.Labels,
			StartTime:   startTime,
			EndTime:     endTime,
			Limit:       size,
			Cursor:      cursor,
			Order:       order,
		}

		resp, err := client.QueryLogs(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("query logs: %w", err)
		}
		page := resp.GetEntries()
		entries = append(entries, page...)
		cursor = resp.GetNextCursor()
		if cursor == "" || (limit > 0 && len(entries) >= int(limit)) {
			return entries, nil
		}
	}
}

func (c *Client) Tail(ctx context.Context, filter Filter, since time.Time, emit Emit) error {
	client := c.NewClient(c.URL, c.Token)
	seen := newOverlap(since)
	for {
		start := timestamppb.New(seen.latest)
		req := &observabilityv1.TailLogsRequest{
			WorkspaceId: filter.WorkspaceID,
			ResourceIds: filter.ResourceIDs,
			Labels:      filter.Labels,
			Since:       start,
		}

		stream, err := client.TailLogs(ctx, req)
		if err != nil {
			return tailError(ctx, err)
		}
		var receiveErr error
		for {
			msg, err := stream.Receive()
			if err != nil {
				receiveErr = err
				break
			}
			entry := msg.GetEntry()
			if entry == nil || !seen.fresh(entry) {
				continue
			}
			if emitErr := emit(entry); emitErr != nil {
				closeErr := stream.Close()
				return errors.Join(emitErr, closeErr)
			}
		}
		if errors.Is(receiveErr, io.EOF) {
			receiveErr = nil
		}
		streamErr := receiveErr
		closeErr := stream.Close()
		if stopped(ctx) {
			return nil
		}
		if streamErr != nil {
			joined := errors.Join(streamErr, closeErr)
			return tailError(ctx, joined)
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

func tailError(ctx context.Context, err error) error {
	if stopped(ctx) {
		return nil
	}
	return fmt.Errorf("tail logs: %w", err)
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
