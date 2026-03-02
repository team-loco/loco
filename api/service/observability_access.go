package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	observabilityv1 "github.com/team-loco/loco/proto/loco/observability/v1"
)

// ObservabilityAccessServer implements the ObservabilityAccessService on the control plane.
type ObservabilityAccessServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	tvm     *tvm.VendingMachine
}

func NewObservabilityAccessServer(db *pgxpool.Pool, queries genDb.Querier, tvm *tvm.VendingMachine) *ObservabilityAccessServer {
	return &ObservabilityAccessServer{db: db, queries: queries, tvm: tvm}
}

// GetObservabilityAccess returns the regional proxy endpoints for the workspace.
// The client uses its existing TVM token directly when talking to the proxy.
func (s *ObservabilityAccessServer) GetObservabilityAccess(
	ctx context.Context,
	req *connect.Request[observabilityv1.GetObservabilityAccessRequest],
) (*connect.Response[observabilityv1.GetObservabilityAccessResponse], error) {
	msg := req.Msg

	if msg.GetWorkspaceId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	workspaceID, err := uuid.Parse(msg.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace_id: %w", err))
	}

	if verifyErr := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   workspaceID,
		Scope:      genDb.ScopeRead,
	}); verifyErr != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions"))
	}

	clusters, err := s.queries.GetClustersByWorkspaceDeployments(ctx, workspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up clusters", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to look up clusters"))
	}

	clusterAccess := make([]*observabilityv1.ClusterAccess, 0, len(clusters))
	for _, c := range clusters {
		if !c.ObservabilityProxyEndpoint.Valid || c.ObservabilityProxyEndpoint.String == "" {
			continue
		}
		clusterAccess = append(clusterAccess, &observabilityv1.ClusterAccess{
			ClusterId: c.ID.String(),
			ProxyUrl:  c.ObservabilityProxyEndpoint.String,
			Region:    c.Region,
		})
	}

	return connect.NewResponse(&observabilityv1.GetObservabilityAccessResponse{
		Clusters: clusterAccess,
	}), nil
}

// CheckPermission is called by the observability proxy to validate whether a token
// has the requested permission on an entity.
func (s *ObservabilityAccessServer) CheckPermission(
	ctx context.Context,
	req *connect.Request[observabilityv1.CheckPermissionRequest],
) (*connect.Response[observabilityv1.CheckPermissionResponse], error) {
	msg := req.Msg

	if msg.GetToken() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token is required"))
	}
	if msg.GetEntityId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("entity_id is required"))
	}

	entityID, err := uuid.Parse(msg.GetEntityId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid entity_id: %w", err))
	}

	var entityType genDb.EntityType
	switch msg.GetEntityType() {
	case "workspace":
		entityType = genDb.EntityTypeWorkspace
	case "resource":
		entityType = genDb.EntityTypeResource
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported entity_type: %s", msg.GetEntityType()))
	}

	var scope genDb.Scope
	switch msg.GetScope() {
	case "read":
		scope = genDb.ScopeRead
	case "write":
		scope = genDb.ScopeWrite
	case "admin":
		scope = genDb.ScopeAdmin
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unsupported scope: %s", msg.GetScope()))
	}

	_, scopes, err := s.tvm.GetToken(ctx, msg.GetToken())
	if err != nil {
		return connect.NewResponse(&observabilityv1.CheckPermissionResponse{Allowed: false}), nil
	}

	allowed := s.tvm.VerifyWithGivenEntityScopes(ctx, scopes, genDb.EntityScope{
		EntityType: entityType,
		EntityID:   entityID,
		Scope:      scope,
	}) == nil

	return connect.NewResponse(&observabilityv1.CheckPermissionResponse{Allowed: allowed}), nil
}
