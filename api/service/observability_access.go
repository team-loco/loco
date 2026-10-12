package service

import (
	"context"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

// ObservabilityAccessServer implements the ObservabilityAccessService on the control plane.
type ObservabilityAccessServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	tvm     *tvm.VendingMachine
}

func NewObservabilityAccessServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	vendingMachine *tvm.VendingMachine,
) *ObservabilityAccessServer {
	return &ObservabilityAccessServer{db: db, queries: queries, tvm: vendingMachine, authz: authz.New(db, queries)}
}

// GetObservabilityAccess returns the regional proxy endpoints for the workspace.
// The client uses its existing TVM token directly when talking to the proxy.
func (s *ObservabilityAccessServer) GetObservabilityAccess(
	ctx context.Context,
	req *observabilityv1.GetObservabilityAccessRequest,
) (*observabilityv1.GetObservabilityAccessResponse, error) {
	if req.GetWorkspaceId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "workspace_id is required")
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "not authenticated")
	}

	workspaceID, err := uuid.Parse(req.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidWorkspaceID).WithCause(err)
	}

	if verifyErr := s.authz.Check(ctx, entityScopes, genDb.EntityScope{
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   workspaceID,
		Scope:      genDb.ScopeRead,
	}); verifyErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, "insufficient permissions")
	}

	clusters, err := s.queries.GetObservabilityClustersForWorkspace(ctx, workspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up clusters", "error", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to look up clusters")
	}

	clusterAccess := make([]*observabilityv1.ClusterAccess, 0, len(clusters))
	for _, c := range clusters {
		if c.ObservabilityProxyEndpoint == nil || *c.ObservabilityProxyEndpoint == "" {
			continue
		}
		clusterAccess = append(clusterAccess, &observabilityv1.ClusterAccess{
			ClusterId: c.ID.String(),
			ProxyUrl:  *c.ObservabilityProxyEndpoint,
			Region:    c.Region,
		})
	}

	return &observabilityv1.GetObservabilityAccessResponse{
		Clusters: clusterAccess,
	}, nil
}
