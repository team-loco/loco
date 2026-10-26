package service

import (
	"context"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

type fakeStore struct {
	err   error
	calls int
}

func (f *fakeStore) QueryLogs(
	context.Context, string, []string, time.Time, time.Time, string, []string, map[string]string,
	int32, string, observabilityv1.LogOrder, int, chClient.LogScope,
) ([]*observabilityv1.LogEntry, string, error) {
	f.calls++
	return nil, "", f.err
}

func (f *fakeStore) QueryTailLogPage(
	context.Context, *observabilityv1.TailLogsRequest, time.Time, int32, int, chClient.TailLogCursor,
) (chClient.TailLogPage, error) {
	f.calls++
	return chClient.TailLogPage{}, f.err
}

func (f *fakeStore) QueryMetrics(
	context.Context, string, string, string, []string, time.Time, time.Time, string, int32, string, int,
) ([]*observabilityv1.MetricSeries, error) {
	f.calls++
	return nil, f.err
}

func (f *fakeStore) QueryTraces(
	context.Context, *observabilityv1.QueryTracesRequest, int32, int,
) (*observabilityv1.QueryTracesResponse, error) {
	f.calls++
	return &observabilityv1.QueryTracesResponse{}, f.err
}
