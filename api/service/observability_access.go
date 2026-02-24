package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	observabilityv1 "github.com/team-loco/loco/proto/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	observabilityTokenDuration = 30 * time.Minute
	observabilityTokenName     = "observability-access"
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

// GetObservabilityAccess mints a short-lived TVM token for observability access
// and returns the regional proxy endpoints.
func (s *ObservabilityAccessServer) GetObservabilityAccess(
	ctx context.Context,
	req *connect.Request[observabilityv1.GetObservabilityAccessRequest],
) (*connect.Response[observabilityv1.GetObservabilityAccessResponse], error) {
	msg := req.Msg

	if msg.GetWorkspaceId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workspace_id is required"))
	}

	// Extract authenticated user from context
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	// Verify the user has read access to the workspace
	workspaceID, err := uuid.Parse(msg.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace_id: %w", err))
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   workspaceID,
		Scope:      genDb.ScopeRead,
	}); err != nil {
		slog.WarnContext(ctx, "unauthorized observability access", "workspace_id", msg.GetWorkspaceId(), "user", entity.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions"))
	}

	// Build scopes for the observability token
	obsScopes := []genDb.EntityScope{
		{
			EntityType: genDb.EntityTypeWorkspace,
			EntityID:   workspaceID,
			Scope:      genDb.ScopeRead,
		},
	}

	// If specific resources requested, add resource-level scopes
	for _, rid := range msg.GetResourceIds() {
		resourceUUID, err := uuid.Parse(rid)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_id: %s", rid))
		}
		obsScopes = append(obsScopes, genDb.EntityScope{
			EntityType: genDb.EntityTypeResource,
			EntityID:   resourceUUID,
			Scope:      genDb.ScopeRead,
		})
	}

	// Issue a short-lived TVM token
	token, err := s.tvm.Issue(
		ctx,
		observabilityTokenName,
		entity.ID.String(),
		genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: workspaceID},
		obsScopes,
		observabilityTokenDuration,
	)
	if err != nil {
		if errors.Is(err, tvm.ErrInsufficentPermissions) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions"))
		}
		slog.ErrorContext(ctx, "failed to issue observability token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to issue token"))
	}

	// Look up clusters that have deployments for this workspace
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
			ClusterId: c.ID,
			ProxyUrl:  c.ObservabilityProxyEndpoint.String,
			Region:    c.Region,
		})
	}

	expiresAt := time.Now().Add(observabilityTokenDuration)

	slog.InfoContext(ctx, "issued observability access",
		"workspace_id", msg.GetWorkspaceId(),
		"clusters", len(clusterAccess),
		"user", entity.ID.String(),
	)

	return connect.NewResponse(&observabilityv1.GetObservabilityAccessResponse{
		Token:     token,
		ExpiresAt: timestamppb.New(expiresAt),
		Clusters:  clusterAccess,
	}), nil
}

// ValidateObservabilityToken is called by the observability proxy to validate a token.
// It authenticates the proxy using a separate proxy auth token (checked by the auth interceptor).
func (s *ObservabilityAccessServer) ValidateObservabilityToken(
	ctx context.Context,
	req *connect.Request[observabilityv1.ValidateObservabilityTokenRequest],
) (*connect.Response[observabilityv1.ValidateObservabilityTokenResponse], error) {
	token := req.Msg.GetToken()
	if token == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token is required"))
	}

	// Look up the token in TVM
	tokenData, err := s.queries.GetToken(ctx, token)
	if err != nil {
		slog.WarnContext(ctx, "observability token not found", "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
	}

	if time.Now().After(tokenData.ExpiresAt) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("token expired"))
	}

	// Extract workspace_id and resource_ids from the token's scopes
	var workspaceID string
	var resourceIDs []string
	var scopes []string

	for _, scope := range tokenData.Scopes {
		scopes = append(scopes, string(scope.Scope))
		switch scope.EntityType {
		case genDb.EntityTypeWorkspace:
			workspaceID = scope.EntityID.String()
		case genDb.EntityTypeResource:
			resourceIDs = append(resourceIDs, scope.EntityID.String())
		}
	}

	return connect.NewResponse(&observabilityv1.ValidateObservabilityTokenResponse{
		WorkspaceId: workspaceID,
		ResourceIds: resourceIDs,
		Scopes:      scopes,
		ExpiresAt:   timestamppb.New(tokenData.ExpiresAt),
	}), nil
}
