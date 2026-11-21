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

// requestSpan opens the span an agent message about a placement or build runs in. Heartbeats and
// inventories repeat on a timer and get a no-op span.
func (s *AgentServer) requestSpan(
	ctx context.Context,
	clusterID uuid.UUID,
	msg *agentv1.SyncRequest,
) (context.Context, trace.Span) {
	kind := trace.SpanKindConsumer
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncRequest_Applied:
		return s.placementSpan(
			ctx,
			clusterID,
			syncAppliedSpan,
			kind,
			m.Applied.GetPlacementId(),
			m.Applied.GetRevision(),
		)
	case *agentv1.SyncRequest_Status:
		return s.placementSpan(ctx, clusterID, syncStatusSpan, kind,
			m.Status.GetPlacementId(), m.Status.GetObservedRevision())
	case *agentv1.SyncRequest_BuildStatus:
		return s.buildSpan(ctx, clusterID, syncBuildStatusSpan, kind, m.BuildStatus.GetBuildId())
	default:
		return ctx, noop.Span{}
	}
}

// responseSpan opens the span a message to the agent about a placement or build is sent in.
func (s *AgentServer) responseSpan(
	ctx context.Context,
	clusterID uuid.UUID,
	msg *agentv1.SyncResponse,
) (context.Context, trace.Span) {
	kind := trace.SpanKindProducer
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncResponse_Apply:
		return s.placementSpan(ctx, clusterID, syncApplySpan, kind, m.Apply.GetPlacementId(), m.Apply.GetRevision())
	case *agentv1.SyncResponse_Delete:
		return s.placementSpan(ctx, clusterID, syncDeleteSpan, kind, m.Delete.GetPlacementId(), m.Delete.GetRevision())
	case *agentv1.SyncResponse_StartBuild:
		return s.buildSpan(ctx, clusterID, syncStartBuildSpan, kind, m.StartBuild.GetBuildId())
	case *agentv1.SyncResponse_CancelBuild:
		return s.buildSpan(ctx, clusterID, syncCancelBuildSpan, kind, m.CancelBuild.GetBuildId())
	default:
		return ctx, noop.Span{}
	}
}

// placementSpan starts a root span linked to the request that created the placement's deployment.
func (s *AgentServer) placementSpan(
	ctx context.Context,
	clusterID uuid.UUID,
	name string,
	kind trace.SpanKind,
	placementID string,
	revision int64,
) (context.Context, trace.Span) {
	return tracing.StartLinked(ctx, name, syncOrigin(ctx, s.queries.GetPlacementTraceparent, placementID),
		trace.WithSpanKind(kind), trace.WithAttributes(
			attribute.String(syncClusterIDAttr, clusterID.String()),
			attribute.String(syncPlacementIDAttr, placementID),
			attribute.Int64(syncRevisionAttr, revision),
		))
}

// buildSpan starts a root span linked to the request that created the build.
func (s *AgentServer) buildSpan(
	ctx context.Context,
	clusterID uuid.UUID,
	name string,
	kind trace.SpanKind,
	buildID string,
) (context.Context, trace.Span) {
	return tracing.StartLinked(ctx, name, syncOrigin(ctx, s.queries.GetBuildTraceparent, buildID),
		trace.WithSpanKind(kind), trace.WithAttributes(
			attribute.String(syncClusterIDAttr, clusterID.String()),
			attribute.String(syncBuildIDAttr, buildID),
		))
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
