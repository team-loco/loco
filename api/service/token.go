package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	tokenv1 "github.com/team-loco/loco/shared/proto/token/v1"
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

// CreateToken issues a new token for a specific entity with defined scopes
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

	if r.GetExpiresInSec() <= 0 || r.GetExpiresInSec() > int64(s.tvm.Cfg.MaxTokenDuration.Seconds()) {
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

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   r.GetEntityId(),
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeWrite,
	}); err != nil {
		slog.WarnContext(ctx, "unauthorized to create token for entity", "entityType", targetEntity.Type, "entityId", targetEntity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	dbScopes := make([]genDb.EntityScope, len(r.GetScopes()))
	for i, scope := range r.GetScopes() {
		dbScopes[i] = genDb.EntityScope{
			EntityType: protoEntityTypeToDb(scope.GetEntityType()),
			EntityID:   scope.GetEntityId(),
			Scope:      protoScopeToDb(scope.GetScope()),
		}
	}

	duration := time.Duration(r.GetExpiresInSec()) * time.Second
	token, err := s.tvm.Issue(ctx, r.GetName(), entity.ID, targetEntity, dbScopes, duration)
	if err != nil {
		if errors.Is(err, tvm.ErrInsufficentPermissions) {
			slog.WarnContext(ctx, "user lacks permissions for requested scopes", "user_id", entity.ID)
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		slog.ErrorContext(ctx, "failed to issue token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to issue token: %w", err))
	}

	tokenData, err := s.queries.GetTokenByName(ctx, genDb.GetTokenByNameParams{
		Name:       r.GetName(),
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch created token metadata", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to fetch token metadata: %w", err))
	}

	tokenMetadata := dbTokenGetRowToProto(tokenData)

	slog.InfoContext(ctx, "created token", "name", r.GetName(), "entityType", targetEntity.Type, "entityId", targetEntity.ID)

	return connect.NewResponse(&tokenv1.CreateTokenResponse{
		Token:         token,
		TokenMetadata: tokenMetadata,
	}), nil
}

// ListTokens lists all tokens associated with an entity
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

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   r.GetEntityId(),
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeRead,
	}); err != nil {
		slog.WarnContext(ctx, "unauthorized to list tokens for entity", "entityType", targetEntity.Type, "entityId", targetEntity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	tokens, err := s.tvm.ListTokensForEntity(ctx, targetEntity)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list tokens", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list tokens: %w", err))
	}

	protoTokens := make([]*tokenv1.Token, len(tokens))
	for i, token := range tokens {
		protoTokens[i] = dbTokenListRowToProto(token)
	}

	return connect.NewResponse(&tokenv1.ListTokensResponse{
		Tokens: protoTokens,
	}), nil
}

// GetToken retrieves metadata for a specific token
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

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   r.GetEntityId(),
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeRead,
	}); err != nil {
		slog.WarnContext(ctx, "unauthorized to get token for entity", "entityType", targetEntity.Type, "entityId", targetEntity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	token, err := s.queries.GetTokenByName(ctx, genDb.GetTokenByNameParams{
		Name:       r.GetName(),
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
	})
	if err != nil {
		slog.WarnContext(ctx, "token not found", "name", r.GetName())
		return nil, connect.NewError(connect.CodeNotFound, ErrTokenNotFound)
	}

	return connect.NewResponse(&tokenv1.GetTokenResponse{
		Token: dbTokenGetRowToProto(token),
	}), nil
}

// RevokeToken revokes/deletes a token
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

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(r.GetEntityType()),
		ID:   r.GetEntityId(),
	}

	hasWritePermission := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeWrite,
	}) == nil

	isOwnToken := targetEntity.Type == genDb.EntityTypeUser && targetEntity.ID == entity.ID

	if !hasWritePermission && !isOwnToken {
		slog.WarnContext(ctx, "unauthorized to revoke token", "entityType", targetEntity.Type, "entityId", targetEntity.ID, "user_id", entity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("insufficient permissions to revoke token"))
	}

	err := s.queries.DeleteTokenByNameAndEntity(ctx, genDb.DeleteTokenByNameAndEntityParams{
		Name:       r.GetName(),
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to revoke token: %w", err))
	}

	slog.InfoContext(ctx, "revoked token", "name", r.GetName(), "entityType", targetEntity.Type, "entityId", targetEntity.ID)

	return connect.NewResponse(&tokenv1.RevokeTokenResponse{}), nil
}

// Helper functions

func dbTokenListRowToProto(token genDb.ListTokensForEntityRow) *tokenv1.Token {
	return convertTokenToProto(token.Name, token.EntityType, token.EntityID, token.Scopes, token.ExpiresAt)
}

func dbTokenGetRowToProto(token genDb.GetTokenByNameRow) *tokenv1.Token {
	return convertTokenToProto(token.Name, token.EntityType, token.EntityID, token.Scopes, token.ExpiresAt)
}

func convertTokenToProto(name string, entityType genDb.EntityType, entityID int64, dbScopes []genDb.EntityScope, expiresAt time.Time) *tokenv1.Token {
	scopes := make([]*tokenv1.EntityScope, len(dbScopes))
	for i, scope := range dbScopes {
		scopes[i] = &tokenv1.EntityScope{
			Scope:      dbScopeToProto(scope.Scope),
			EntityType: dbEntityTypeToProto(scope.EntityType),
			EntityId:   scope.EntityID,
		}
	}

	return &tokenv1.Token{
		Name:       name,
		EntityType: dbEntityTypeToProto(entityType),
		EntityId:   entityID,
		Scopes:     scopes,
		ExpiresAt:  timeutil.ParsePostgresTimestamp(expiresAt),
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
		return genDb.EntityTypeUser // default fallback
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
		return genDb.ScopeRead // default fallback
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
