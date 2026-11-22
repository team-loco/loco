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
		return placementSpan(
			ctx,
			clusterID,
			syncAppliedSpan,
			kind,
			s.queries.GetPlacementTraceparent,
			m.Applied.GetPlacementId(),
			m.Applied.GetRevision(),
		)
	case *agentv1.SyncRequest_Status:
		return placementSpan(ctx, clusterID, syncStatusSpan, kind, s.queries.GetPlacementTraceparent,
			m.Status.GetPlacementId(), m.Status.GetObservedRevision())
	case *agentv1.SyncRequest_BuildStatus:
		return buildSpan(ctx, clusterID, syncBuildStatusSpan, kind, s.queries.GetBuildTraceparent,
			m.BuildStatus.GetBuildId())
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
		return placementSpan(ctx, clusterID, syncApplySpan, kind, sent(m.Apply.GetTraceparent()),
			m.Apply.GetPlacementId(), m.Apply.GetRevision())
	case *agentv1.SyncResponse_Delete:
		return placementSpan(ctx, clusterID, syncDeleteSpan, kind, s.queries.GetPlacementTraceparent,
			m.Delete.GetPlacementId(), m.Delete.GetRevision())
	case *agentv1.SyncResponse_StartBuild:
		return buildSpan(ctx, clusterID, syncStartBuildSpan, kind, sent(m.StartBuild.GetTraceparent()),
			m.StartBuild.GetBuildId())
	case *agentv1.SyncResponse_CancelBuild:
		return buildSpan(ctx, clusterID, syncCancelBuildSpan, kind, s.queries.GetBuildTraceparent,
			m.CancelBuild.GetBuildId())
	default:
		return ctx, noop.Span{}
	}
}

// placementSpan starts a root span linked to the request that created the placement's deployment.
func placementSpan(
	ctx context.Context,
	clusterID uuid.UUID,
	name string,
	kind trace.SpanKind,
	origin originFunc,
	placementID string,
	revision int64,
) (context.Context, trace.Span) {
	return syncSpan(ctx, name, kind, origin, placementID,
		attribute.String(syncClusterIDAttr, clusterID.String()),
		attribute.String(syncPlacementIDAttr, placementID),
		attribute.Int64(syncRevisionAttr, revision),
	)
}

// buildSpan starts a root span linked to the request that created the build.
func buildSpan(
	ctx context.Context,
	clusterID uuid.UUID,
	name string,
	kind trace.SpanKind,
	origin originFunc,
	buildID string,
) (context.Context, trace.Span) {
	return syncSpan(ctx, name, kind, origin, buildID,
		attribute.String(syncClusterIDAttr, clusterID.String()),
		attribute.String(syncBuildIDAttr, buildID),
	)
}

// originFunc reads the traceparent of the request that created a placement's deployment or a build.
type originFunc func(context.Context, uuid.UUID) (*string, error)

// sent is the origin of a message that already carries its traceparent.
func sent(traceparent string) originFunc {
	return func(context.Context, uuid.UUID) (*string, error) {
		return &traceparent, nil
	}
}

// syncSpan looks up the originating request only for a recording span, so untraced installs skip the query.
func syncSpan(
	ctx context.Context,
	name string,
	kind trace.SpanKind,
	lookup originFunc,
	rawID string,
	attrs ...attribute.KeyValue,
) (context.Context, trace.Span) {
	ctx, span := tracing.StartRoot(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
	if !span.IsRecording() {
		return ctx, span
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return ctx, span
	}
	traceparent, err := lookup(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.WarnContext(ctx, "failed to look up the trace that started sync work", "id", id, "error", err)
	}
	tracing.Link(span, traceparent)
	return ctx, span
}
