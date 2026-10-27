package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (ss *syncSession) recordUsage(ctx context.Context, report *agentv1.UsageReport) error {
	if err := protovalidate.Validate(report); err != nil {
		slog.ErrorContext(ctx, "rejected an invalid usage report", "cluster_id", ss.clusterID, "error", err)
		return nil
	}
	now := time.Now()
	if err := ss.server.recordUsageReport(ctx, ss.clusterID, report, now); err != nil {
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

func (s *AgentServer) recordUsageReport(
	ctx context.Context,
	clusterID uuid.UUID,
	report *agentv1.UsageReport,
	now time.Time,
) error {
	observedAt := report.GetObservedAt().AsTime()
	reportedAt := earliest(observedAt, now)
	params := workloadRunParams(clusterID, reportedAt, report.GetRuns())
	return withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if err := qtx.UpsertWorkloadRuns(ctx, params); err != nil {
			return fmt.Errorf("upsert workload runs: %w", err)
		}
		if err := qtx.PruneClusterUsageReports(ctx, clusterID); err != nil {
			return fmt.Errorf("prune usage reports: %w", err)
		}
		return qtx.RecordClusterUsageReport(ctx, genDb.RecordClusterUsageReportParams{
			ClusterID:  clusterID,
			ReportedAt: reportedAt,
		})
	})
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
