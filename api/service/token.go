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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
)

var (
	ErrTokenNotFound        = errors.New("token not found")
	ErrTokenAlreadyExists   = errors.New("token with this name already exists for the entity")
	ErrInvalidTokenDuration = errors.New("invalid token duration")
	ErrInvalidScopes        = errors.New("invalid scopes")
	ErrTokenUnauthorized    = errors.New("unauthorized")
)

// TokenServer implements the TokenService gRPC server
type TokenServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	tvm     *tvm.VendingMachine
}

// NewTokenServer creates a new TokenServer instance
func NewTokenServer(db *pgxpool.Pool, queries genDb.Querier, tvm *tvm.VendingMachine) *TokenServer {
	return &TokenServer{db: db, queries: queries, tvm: tvm}
}

// CreateToken issues a new API token for a specific entity with defined scopes
func (s *TokenServer) CreateToken(
	ctx context.Context,
	req *connect.Request[tokenv1.CreateTokenRequest],
) (*connect.Response[tokenv1.CreateTokenResponse], error) {
	r := req.Msg

	if r.GetName() == "" {
		slog.ErrorContext(ctx, "invalid request: name is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	if r.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("entity_type is required"))
	}

	if r.GetExpiresInSec() <= 0 || r.GetExpiresInSec() > int64(s.tvm.Cfg.MaxAPITokenDuration.Seconds()) {
		slog.ErrorContext(ctx, "invalid token duration", "expires_in_sec", r.GetExpiresInSec())
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidTokenDuration)
	}

	if len(r.GetScopes()) == 0 {
		slog.ErrorContext(ctx, "invalid request: at least one scope is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidScopes)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	entityId, err := uuid.Parse(r.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", r.GetEntityId(), "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid entity id: %w", err))
	}
	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   entityId,
	}

	if verifyErr := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeWrite,
	}); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to create token for entity", "entityType", targetEntity.Type, "entityId", targetEntity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	dbScopes := make([]genDb.EntityScope, len(r.GetScopes()))
	for i, scope := range r.GetScopes() {
		scopeEntityId, scopeErr := uuid.Parse(scope.GetEntityId())
		if scopeErr != nil {
			slog.ErrorContext(ctx, "invalid scope entity id format", "entityId", scope.GetEntityId(), "error", scopeErr)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid scope entity id: %w", scopeErr))
		}
		dbScopes[i] = genDb.EntityScope{
			EntityType: protoEntityTypeToDb(scope.GetEntityType()),
			EntityID:   scopeEntityId,
			Scope:      protoScopeToDb(scope.GetScope()),
		}
	}

	duration := time.Duration(r.GetExpiresInSec()) * time.Second
	token, err := s.tvm.Issue(ctx, r.GetName(), entity.ID.String(), targetEntity, dbScopes, duration)
	if err != nil {
		if errors.Is(err, tvm.ErrInsufficentPermissions) {
			slog.WarnContext(ctx, "user lacks permissions for requested scopes", "user_id", entity.ID.String())
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		slog.ErrorContext(ctx, "failed to issue token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to issue token"))
	}

	// fetch metadata using the token we just issued
	tokenHash := hashToken(token)
	tokenData, err := s.queries.GetAPIToken(ctx, tokenHash)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch created token metadata", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to fetch token metadata"))
	}

	slog.InfoContext(ctx, "created token", "name", r.GetName(), "entityType", targetEntity.Type, "entityId", targetEntity.ID)

	return connect.NewResponse(&tokenv1.CreateTokenResponse{
		Token:         token,
		TokenMetadata: apiTokenRowToProto(tokenData.Name, tokenData.EntityType, tokenData.EntityID, tokenData.Scopes, tokenData.CreatedAt, tokenData.ExpiresAt, tokenData.LastUsedAt),
	}), nil
}

// ListTokens lists all API tokens associated with an entity
func (s *TokenServer) ListTokens(
	ctx context.Context,
	req *connect.Request[tokenv1.ListTokensRequest],
) (*connect.Response[tokenv1.ListTokensResponse], error) {
	r := req.Msg

	if r.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("entity_type is required"))
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	entityId, err := uuid.Parse(r.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", r.GetEntityId())
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid entity id: %w", err))
	}

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   entityId,
	}

	if verifyErr := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeRead,
	}); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to list tokens for entity", "entityType", targetEntity.Type, "entityId", targetEntity.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	tokens, err := s.tvm.ListAPITokensForEntity(ctx, targetEntity)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list tokens", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list tokens"))
	}

	protoTokens := make([]*tokenv1.Token, len(tokens))
	for i, t := range tokens {
		protoTokens[i] = apiTokenRowToProto(t.Name, t.EntityType, t.EntityID, t.Scopes, t.CreatedAt, t.ExpiresAt, t.LastUsedAt)
	}

	return connect.NewResponse(&tokenv1.ListTokensResponse{
		Tokens: protoTokens,
	}), nil
}

// GetToken retrieves metadata for a specific token by name and entity
func (s *TokenServer) GetToken(
	ctx context.Context,
	req *connect.Request[tokenv1.GetTokenRequest],
) (*connect.Response[tokenv1.GetTokenResponse], error) {
	r := req.Msg

	if r.GetName() == "" {
		slog.ErrorContext(ctx, "invalid request: name is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	if r.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("entity_type is required"))
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	entityId, err := uuid.Parse(r.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", r.GetEntityId())
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid entity id: %w", err))
	}

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   entityId,
	}

	if verifyErr := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeRead,
	}); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to get token for entity", "entityType", targetEntity.Type, "entityId", targetEntity.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	token, err := s.queries.GetAPITokenByNameAndEntity(ctx, genDb.GetAPITokenByNameAndEntityParams{
		Name:       r.GetName(),
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
	})
	if err != nil {
		slog.WarnContext(ctx, "token not found", "name", r.GetName())
		return nil, connect.NewError(connect.CodeNotFound, ErrTokenNotFound)
	}

	return connect.NewResponse(&tokenv1.GetTokenResponse{
		Token: apiTokenRowToProto(token.Name, token.EntityType, token.EntityID, token.Scopes, token.CreatedAt, token.ExpiresAt, token.LastUsedAt),
	}), nil
}

// RevokeToken revokes/deletes an API token
func (s *TokenServer) RevokeToken(
	ctx context.Context,
	req *connect.Request[tokenv1.RevokeTokenRequest],
) (*connect.Response[tokenv1.RevokeTokenResponse], error) {
	r := req.Msg

	if r.GetName() == "" {
		slog.ErrorContext(ctx, "invalid request: name is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	if r.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("entity_type is required"))
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	entityId, err := uuid.Parse(r.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", r.GetEntityId())
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid entity id: %w", err))
	}

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   entityId,
	}

	hasWritePermission := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeWrite,
	}) == nil

	isOwnToken := targetEntity.Type == genDb.EntityTypeUser && targetEntity.ID == entity.ID

	if !hasWritePermission && !isOwnToken {
		slog.WarnContext(ctx, "unauthorized to revoke token", "entityType", targetEntity.Type, "entityId", targetEntity.ID.String(), "user_id", entity.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to revoke token"))
	}

	if err := s.queries.DeleteAPITokenByNameAndEntity(ctx, genDb.DeleteAPITokenByNameAndEntityParams{
		Name:       r.GetName(),
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to delete token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to revoke token"))
	}

	slog.InfoContext(ctx, "revoked token", "name", r.GetName(), "entityType", targetEntity.Type, "entityId", targetEntity.ID.String())

	return connect.NewResponse(&tokenv1.RevokeTokenResponse{}), nil
}

// GetScopes returns the entity and all scopes the current token has access to.
func (s *TokenServer) GetScopes(
	ctx context.Context,
	req *connect.Request[tokenv1.GetScopesRequest],
) (*connect.Response[tokenv1.GetScopesResponse], error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrTokenUnauthorized)
	}

	protoScopes := make([]*tokenv1.EntityScope, len(entityScopes))
	for i, es := range entityScopes {
		protoScopes[i] = &tokenv1.EntityScope{
			Scope:      dbScopeToProto(es.Scope),
			EntityType: dbEntityTypeToProto(es.EntityType),
			EntityId:   es.EntityID.String(),
		}
	}

	return connect.NewResponse(&tokenv1.GetScopesResponse{
		EntityType: dbEntityTypeToProto(entity.Type),
		EntityId:   entity.ID.String(),
		Scopes:     protoScopes,
	}), nil
}

// CheckPermission validates whether a given token has a specific permission on an entity.
// Intended for service-to-service calls (e.g. the observability proxy).
func (s *TokenServer) CheckPermission(
	ctx context.Context,
	req *connect.Request[tokenv1.CheckPermissionRequest],
) (*connect.Response[tokenv1.CheckPermissionResponse], error) {
	r := req.Msg

	entityID, err := uuid.Parse(r.GetEntityId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid entity_id: %w", err))
	}

	_, scopes, err := s.tvm.GetToken(ctx, r.GetToken())
	if err != nil {
		return connect.NewResponse(&tokenv1.CheckPermissionResponse{Allowed: false}), nil
	}

	allowed := s.tvm.VerifyWithGivenEntityScopes(ctx, scopes, genDb.EntityScope{
		EntityType: protoEntityTypeToDb(r.GetEntityType()),
		EntityID:   entityID,
		Scope:      protoScopeToDb(r.GetScope()),
	}) == nil

	return connect.NewResponse(&tokenv1.CheckPermissionResponse{Allowed: allowed}), nil
}

// Helper functions

func apiTokenRowToProto(name string, entityType genDb.EntityType, entityID uuid.UUID, dbScopes []genDb.EntityScope, createdAt time.Time, expiresAt time.Time, lastUsedAt *time.Time) *tokenv1.Token {
	scopes := make([]*tokenv1.EntityScope, len(dbScopes))
	for i, scope := range dbScopes {
		scopes[i] = &tokenv1.EntityScope{
			Scope:      dbScopeToProto(scope.Scope),
			EntityType: dbEntityTypeToProto(scope.EntityType),
			EntityId:   scope.EntityID.String(),
		}
	}

	var lastUsedProto *timestamppb.Timestamp
	if lastUsedAt != nil {
		lastUsedProto = timeutil.ParsePostgresTimestamp(*lastUsedAt)
	}

	return &tokenv1.Token{
		Name:       name,
		EntityType: dbEntityTypeToProto(entityType),
		EntityId:   entityID.String(),
		Scopes:     scopes,
		CreatedAt:  timeutil.ParsePostgresTimestamp(createdAt),
		ExpiresAt:  timeutil.ParsePostgresTimestamp(expiresAt),
		LastUsedAt: lastUsedProto,
	}
}

func protoEntityTypeToDb(et tokenv1.EntityType) genDb.EntityType {
	switch et {
	case tokenv1.EntityType_ENTITY_TYPE_SYSTEM:
		return genDb.EntityTypeSystem
	case tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION:
		return genDb.EntityTypeOrganization
	case tokenv1.EntityType_ENTITY_TYPE_WORKSPACE:
		return genDb.EntityTypeWorkspace
	case tokenv1.EntityType_ENTITY_TYPE_RESOURCE:
		return genDb.EntityTypeResource
	case tokenv1.EntityType_ENTITY_TYPE_USER:
		return genDb.EntityTypeUser
	default:
		return genDb.EntityTypeUser
	}
}

func dbEntityTypeToProto(et genDb.EntityType) tokenv1.EntityType {
	switch et {
	case genDb.EntityTypeSystem:
		return tokenv1.EntityType_ENTITY_TYPE_SYSTEM
	case genDb.EntityTypeOrganization:
		return tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION
	case genDb.EntityTypeWorkspace:
		return tokenv1.EntityType_ENTITY_TYPE_WORKSPACE
	case genDb.EntityTypeResource:
		return tokenv1.EntityType_ENTITY_TYPE_RESOURCE
	case genDb.EntityTypeUser:
		return tokenv1.EntityType_ENTITY_TYPE_USER
	default:
		return tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED
	}
}

func protoScopeToDb(s tokenv1.Scope) genDb.Scope {
	switch s {
	case tokenv1.Scope_SCOPE_READ:
		return genDb.ScopeRead
	case tokenv1.Scope_SCOPE_WRITE:
		return genDb.ScopeWrite
	case tokenv1.Scope_SCOPE_ADMIN:
		return genDb.ScopeAdmin
	default:
		return genDb.ScopeRead
	}
}

func dbScopeToProto(s genDb.Scope) tokenv1.Scope {
	switch s {
	case genDb.ScopeRead:
		return tokenv1.Scope_SCOPE_READ
	case genDb.ScopeWrite:
		return tokenv1.Scope_SCOPE_WRITE
	case genDb.ScopeAdmin:
		return tokenv1.Scope_SCOPE_ADMIN
	default:
		return tokenv1.Scope_SCOPE_UNSPECIFIED
	}
}
