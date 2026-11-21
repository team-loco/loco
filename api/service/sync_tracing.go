package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/team-loco/loco/api/pkg/tracing"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	syncAppliedSpan     = "agent.sync.applied"
	syncStatusSpan      = "agent.sync.status"
	syncBuildStatusSpan = "agent.sync.build_status"
	syncApplySpan       = "agent.sync.apply"
	syncDeleteSpan      = "agent.sync.delete"
	syncStartBuildSpan  = "agent.sync.start_build"
	syncCancelBuildSpan = "agent.sync.cancel_build"

	syncClusterIDAttr   = "loco.cluster.id"
	syncPlacementIDAttr = "loco.placement.id"
	syncRevisionAttr    = "loco.placement.revision"
	syncBuildIDAttr     = "loco.build.id"
)

// syncSubject is the placement or build a Sync message is about.
type syncSubject struct {
	span        string
	placementID string
	revision    int64
	buildID     string
}

func requestSubject(msg *agentv1.SyncRequest) (syncSubject, bool) {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncRequest_Applied:
		return syncSubject{
			span: syncAppliedSpan, placementID: m.Applied.GetPlacementId(), revision: m.Applied.GetRevision(),
		}, true
	case *agentv1.SyncRequest_Status:
		return syncSubject{
			span: syncStatusSpan, placementID: m.Status.GetPlacementId(), revision: m.Status.GetObservedRevision(),
		}, true
	case *agentv1.SyncRequest_BuildStatus:
		return syncSubject{span: syncBuildStatusSpan, buildID: m.BuildStatus.GetBuildId()}, true
	default:
		return syncSubject{}, false
	}
}

func responseSubject(msg *agentv1.SyncResponse) (syncSubject, bool) {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncResponse_Apply:
		return syncSubject{
			span: syncApplySpan, placementID: m.Apply.GetPlacementId(), revision: m.Apply.GetRevision(),
		}, true
	case *agentv1.SyncResponse_Delete:
		return syncSubject{
			span: syncDeleteSpan, placementID: m.Delete.GetPlacementId(), revision: m.Delete.GetRevision(),
		}, true
	case *agentv1.SyncResponse_StartBuild:
		return syncSubject{span: syncStartBuildSpan, buildID: m.StartBuild.GetBuildId()}, true
	case *agentv1.SyncResponse_CancelBuild:
		return syncSubject{span: syncCancelBuildSpan, buildID: m.CancelBuild.GetBuildId()}, true
	default:
		return syncSubject{}, false
	}
}

// startSyncSpan opens a root span for one Sync message about a placement or build, linked to the
// request that created its deployment or build. Heartbeats and inventories repeat on a timer and get
// a no-op span.
func (s *AgentServer) startSyncSpan(
	ctx context.Context,
	clusterID uuid.UUID,
	subject syncSubject,
	traced bool,
	kind trace.SpanKind,
) (context.Context, trace.Span) {
	if !traced {
		return ctx, noop.Span{}
	}
	attrs := []attribute.KeyValue{attribute.String(syncClusterIDAttr, clusterID.String())}
	var traceparent *string
	switch {
	case subject.placementID != "":
		attrs = append(attrs,
			attribute.String(syncPlacementIDAttr, subject.placementID),
			attribute.Int64(syncRevisionAttr, subject.revision),
		)
		traceparent = syncOrigin(ctx, s.queries.GetPlacementTraceparent, subject.placementID)
	case subject.buildID != "":
		attrs = append(attrs, attribute.String(syncBuildIDAttr, subject.buildID))
		traceparent = syncOrigin(ctx, s.queries.GetBuildTraceparent, subject.buildID)
	}
	return tracing.StartLinked(ctx, subject.span, traceparent,
		trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
}

func syncOrigin(
	ctx context.Context,
	lookup func(context.Context, uuid.UUID) (*string, error),
	rawID string,
) *string {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil
	}
	traceparent, err := lookup(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.WarnContext(ctx, "failed to look up the trace that started sync work", "id", id, "error", err)
	}
	return traceparent
}
