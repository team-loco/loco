package service

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	feedWorkloadRuns = "workload_runs"
	feedEgress       = "egress"
)

func (ss *syncSession) recordUsage(ctx context.Context, report *agentv1.UsageReport) error {
	if err := protovalidate.Validate(report); err != nil {
		slog.ErrorContext(ctx, "rejected an invalid usage report", "cluster_id", ss.clusterID, "error", err)
		return nil
	}
	if err := ss.server.recordUsageReport(ctx, ss.clusterID, report); err != nil {
		slog.ErrorContext(ctx, "failed to record usage report", "cluster_id", ss.clusterID, "error", err)
		return nil
	}
	observedAt := report.GetObservedAt()
	recorded := &agentv1.UsageRecorded{ObservedAt: observedAt}
	ack := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_UsageRecorded{UsageRecorded: recorded}}
	if err := ss.sendFn(ack); err != nil {
		return fmt.Errorf("acknowledge usage report: %w", err)
	}
	return nil
}

func (s *AgentServer) recordUsageReport(ctx context.Context, clusterID uuid.UUID, report *agentv1.UsageReport) error {
	reportedAt := earliest(report.GetObservedAt().AsTime(), time.Now())
	params := workloadRunParams(clusterID, reportedAt, report.GetRuns())
	return withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		// The publisher locks the counter exclusively, so it never runs while a report is half written.
		if err := qtx.ShareUsageStreamCounter(ctx); err != nil {
			return fmt.Errorf("lock usage stream counter: %w", err)
		}
		if err := qtx.UpsertWorkloadRuns(ctx, params); err != nil {
			return fmt.Errorf("upsert workload runs: %w", err)
		}
		if err := recordFeedReport(ctx, qtx, clusterID, feedWorkloadRuns, reportedAt); err != nil {
			return err
		}
		if err := recordEgress(ctx, qtx, clusterID, reportedAt, report.GetEgress()); err != nil {
			return err
		}
		if !report.GetEgressComplete() {
			return nil
		}
		stale := genDb.DeleteStaleEgressCountersParams{ClusterID: clusterID, ObservedAt: reportedAt}
		if err := qtx.DeleteStaleEgressCounters(ctx, stale); err != nil {
			return fmt.Errorf("delete stale egress counters: %w", err)
		}
		return recordFeedReport(ctx, qtx, clusterID, feedEgress, reportedAt)
	})
}

func recordFeedReport(
	ctx context.Context,
	qtx *genDb.Queries,
	clusterID uuid.UUID,
	feed string,
	reportedAt time.Time,
) error {
	record := genDb.RecordClusterUsageReportParams{ClusterID: clusterID, Feed: feed, ReportedAt: reportedAt}
	if err := qtx.RecordClusterUsageReport(ctx, record); err != nil {
		return fmt.Errorf("record %s report: %w", feed, err)
	}
	return nil
}

type egressCounterKey struct {
	pod   uuid.UUID
	route string
}

func recordEgress(
	ctx context.Context,
	qtx *genDb.Queries,
	clusterID uuid.UUID,
	reportedAt time.Time,
	counters []*agentv1.EgressCounter,
) error {
	if len(counters) == 0 {
		return nil
	}
	upsert := genDb.UpsertEgressCountersParams{ClusterID: clusterID, ObservedAt: reportedAt}
	pods := make(map[uuid.UUID]struct{})
	for _, counter := range counters {
		pod := uuid.MustParse(counter.GetGatewayPodUid())
		pods[pod] = struct{}{}
		upsert.GatewayPodUids = append(upsert.GatewayPodUids, pod)
		upsert.Routes = append(upsert.Routes, counter.GetRoute())
		upsert.Bytes = append(upsert.Bytes, counter.GetBytes())
	}
	podUIDs := slices.Collect(maps.Keys(pods))
	lock := genDb.LockEgressCountersParams{ClusterID: clusterID, GatewayPodUids: podUIDs}
	stored, err := qtx.LockEgressCounters(ctx, lock)
	if err != nil {
		return fmt.Errorf("lock egress counters: %w", err)
	}
	previous := make(map[egressCounterKey]genDb.LockEgressCountersRow, len(stored))
	for _, row := range stored {
		previous[egressCounterKey{pod: row.GatewayPodUid, route: row.Route}] = row
	}
	added := genDb.AddEgressUsageParams{ClusterID: clusterID, HourStart: reportedAt.UTC().Truncate(time.Hour)}
	for i, counter := range counters {
		key := egressCounterKey{pod: upsert.GatewayPodUids[i], route: counter.GetRoute()}
		row, seen := previous[key]
		if !seen || !reportedAt.After(row.ObservedAt) {
			continue
		}
		increase := counterIncrease(row.Bytes, counter.GetBytes())
		if increase == 0 {
			continue
		}
		added.OrganizationIds = append(added.OrganizationIds, uuid.MustParse(counter.GetOrganizationId()))
		added.WorkspaceIds = append(added.WorkspaceIds, uuid.MustParse(counter.GetWorkspaceId()))
		added.ResourceIds = append(added.ResourceIds, uuid.MustParse(counter.GetResourceId()))
		added.EnvironmentIds = append(added.EnvironmentIds, uuid.MustParse(counter.GetEnvironmentId()))
		added.Bytes = append(added.Bytes, increase)
	}
	if len(added.Bytes) > 0 {
		if err := qtx.AddEgressUsage(ctx, added); err != nil {
			return fmt.Errorf("add egress usage: %w", err)
		}
	}
	if err := qtx.UpsertEgressCounters(ctx, upsert); err != nil {
		return fmt.Errorf("upsert egress counters: %w", err)
	}
	return nil
}

func counterIncrease(previous, current int64) int64 {
	if current < previous {
		return current
	}
	return current - previous
}

func workloadRunParams(
	clusterID uuid.UUID,
	reportedAt time.Time,
	runs []*agentv1.PodRun,
) genDb.UpsertWorkloadRunsParams {
	params := genDb.UpsertWorkloadRunsParams{
		ClusterID:       clusterID,
		ObservedAt:      reportedAt,
		PodUids:         make([]uuid.UUID, len(runs)),
		OrganizationIds: make([]uuid.UUID, len(runs)),
		WorkspaceIds:    make([]uuid.UUID, len(runs)),
		ResourceIds:     make([]uuid.UUID, len(runs)),
		EnvironmentIds:  make([]uuid.UUID, len(runs)),
		CpuMillicores:   make([]int64, len(runs)),
		MemoryBytes:     make([]int64, len(runs)),
		StartedAts:      make([]time.Time, len(runs)),
		Ended:           make([]bool, len(runs)),
		EndedAts:        make([]time.Time, len(runs)),
	}
	for i, run := range runs {
		params.PodUids[i] = uuid.MustParse(run.GetPodUid())
		params.OrganizationIds[i] = uuid.MustParse(run.GetOrganizationId())
		params.WorkspaceIds[i] = uuid.MustParse(run.GetWorkspaceId())
		params.ResourceIds[i] = uuid.MustParse(run.GetResourceId())
		params.EnvironmentIds[i] = uuid.MustParse(run.GetEnvironmentId())
		params.CpuMillicores[i] = run.GetCpuMillicores()
		params.MemoryBytes[i] = run.GetMemoryBytes()
		startedAt := run.GetStartedAt().AsTime()
		params.StartedAts[i] = earliest(startedAt, reportedAt)
		params.Ended[i] = run.GetEndedAt() != nil
		params.EndedAts[i] = endedAt(run.GetEndedAt(), reportedAt)
	}
	return params
}

func endedAt(ended *timestamppb.Timestamp, reportedAt time.Time) time.Time {
	if ended == nil {
		return reportedAt
	}
	at := ended.AsTime()
	return earliest(at, reportedAt)
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
