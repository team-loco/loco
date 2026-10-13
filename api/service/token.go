package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrTokenNotFound        = errors.New("token not found")
	ErrTokenAlreadyExists   = errors.New("token with this name already exists for the entity")
	ErrInvalidTokenDuration = errors.New("invalid token duration")
	ErrInvalidScopes        = errors.New("invalid scopes")

	errNameRequired       = errors.New("name is required")
	errEntityTypeRequired = errors.New("entity_type is required")
)

// TokenServer implements the TokenService gRPC server
type TokenServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	tvm     *tvm.VendingMachine
}

// NewTokenServer creates a new TokenServer instance
func NewTokenServer(db *pgxpool.Pool, queries genDb.Querier, vendingMachine *tvm.VendingMachine) *TokenServer {
	return &TokenServer{db: db, queries: queries, tvm: vendingMachine, authz: authz.New(db, queries)}
}

// CreateToken issues a new API token for a specific entity with defined scopes
func (s *TokenServer) CreateToken(
	ctx context.Context,
	req *tokenv1.CreateTokenRequest,
) (*tokenv1.CreateTokenResponse, error) {
	if req.GetName() == "" {
		slog.ErrorContext(ctx, "invalid request: name is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, errNameRequired)
	}

	if req.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, errEntityTypeRequired)
	}

	if req.GetExpiresInSec() <= 0 || req.GetExpiresInSec() > int64(s.tvm.Cfg.MaxAPITokenDuration.Seconds()) {
		slog.ErrorContext(ctx, "invalid token duration", "expires_in_sec", req.GetExpiresInSec())
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrInvalidTokenDuration)
	}

	if len(req.GetScopes()) == 0 {
		slog.ErrorContext(ctx, "invalid request: at least one scope is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrInvalidScopes)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityID, err := uuid.Parse(req.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", req.GetEntityId(), "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidEntityID).WithCause(err)
	}
	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(req.GetEntityType()),
		ID:   entityID,
	}

	if verifyErr := s.authz.Check(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeWrite,
	}); verifyErr != nil {
		slog.WarnContext(
			ctx,
			"unauthorized to create token for entity",
			"entityType",
			targetEntity.Type,
			"entityId",
			targetEntity.ID,
		)
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	dbScopes := make([]genDb.EntityScope, len(req.GetScopes()))
	for i, scope := range req.GetScopes() {
		scopeEntityID, scopeErr := uuid.Parse(scope.GetEntityId())
		if scopeErr != nil {
			slog.ErrorContext(ctx, "invalid scope entity id format", "entityId", scope.GetEntityId(), "error", scopeErr)
			return nil, connect.NewError(
				connect.CodeInvalidArgument,
				publicInvalidScopeEntityID,
			).WithCause(scopeErr)
		}
		dbScopes[i] = genDb.EntityScope{
			EntityType: protoEntityTypeToDb(scope.GetEntityType()),
			EntityID:   scopeEntityID,
			Scope:      protoScopeToDb(scope.GetScope()),
		}
	}

	duration := time.Duration(req.GetExpiresInSec()) * time.Second
	var token string
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		var issueErr error
		token, issueErr = s.tvm.WithQueries(qtx).
			Issue(ctx, req.GetName(), entity.ID.String(), targetEntity, dbScopes, duration)
		if issueErr != nil {
			return issueErr
		}
		return events.Record(ctx, qtx, tokenEvent(events.TokenCreated, targetEntity, req.GetName()))
	})
	if err != nil {
		if errors.Is(err, authz.ErrInsufficientPermissions) {
			slog.WarnContext(ctx, "user lacks permissions for requested scopes", "user_id", entity.ID.String())
			return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
		}
		if errors.Is(err, tvm.ErrTokenNameTaken) {
			return nil, rpcerr.New(connect.CodeAlreadyExists, ErrTokenAlreadyExists)
		}
		slog.ErrorContext(ctx, "failed to issue token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to issue token")
	}

	// fetch metadata using the token we just issued
	tokenHash := hashToken(token)
	tokenData, err := s.queries.GetAPIToken(ctx, tokenHash)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch created token metadata", "error", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to fetch token metadata")
	}

	slog.InfoContext(
		ctx,
		"created token",
		"name",
		req.GetName(),
		"entityType",
		targetEntity.Type,
		"entityId",
		targetEntity.ID,
	)

	return &tokenv1.CreateTokenResponse{
		Token: token,
		TokenMetadata: apiTokenRowToProto(
			tokenData.Name,
			tokenData.EntityType,
			tokenData.EntityID,
			tokenData.Scopes,
			tokenData.CreatedAt,
			tokenData.ExpiresAt,
			tokenData.LastUsedAt,
		),
	}, nil
}

// ListTokens lists all API tokens associated with an entity
func (s *TokenServer) ListTokens(
	ctx context.Context,
	req *tokenv1.ListTokensRequest,
) (*tokenv1.ListTokensResponse, error) {
	if req.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, errEntityTypeRequired)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityID, err := uuid.Parse(req.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", req.GetEntityId())
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidEntityID).WithCause(err)
	}

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(req.GetEntityType()),
		ID:   entityID,
	}

	if verifyErr := s.authz.Check(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeRead,
	}); verifyErr != nil {
		slog.WarnContext(
			ctx,
			"unauthorized to list tokens for entity",
			"entityType",
			targetEntity.Type,
			"entityId",
			targetEntity.ID.String(),
		)
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	tokens, err := s.tvm.ListAPITokensForEntity(ctx, targetEntity)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list tokens", "error", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to list tokens")
	}

	protoTokens := make([]*tokenv1.Token, len(tokens))
	for i, t := range tokens {
		protoTokens[i] = apiTokenRowToProto(
			t.Name,
			t.EntityType,
			t.EntityID,
			t.Scopes,
			t.CreatedAt,
			t.ExpiresAt,
			t.LastUsedAt,
		)
	}

	return &tokenv1.ListTokensResponse{
		Tokens: protoTokens,
	}, nil
}

// GetToken retrieves metadata for a specific token by name and entity
func (s *TokenServer) GetToken(
	ctx context.Context,
	req *tokenv1.GetTokenRequest,
) (*tokenv1.GetTokenResponse, error) {
	if req.GetName() == "" {
		slog.ErrorContext(ctx, "invalid request: name is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, errNameRequired)
	}

	if req.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, errEntityTypeRequired)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityID, err := uuid.Parse(req.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", req.GetEntityId())
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidEntityID).WithCause(err)
	}

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(req.GetEntityType()),
		ID:   entityID,
	}

	if verifyErr := s.authz.Check(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeRead,
	}); verifyErr != nil {
		slog.WarnContext(
			ctx,
			"unauthorized to get token for entity",
			"entityType",
			targetEntity.Type,
			"entityId",
			targetEntity.ID.String(),
		)
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	token, err := s.queries.GetAPITokenByNameAndEntity(ctx, genDb.GetAPITokenByNameAndEntityParams{
		Name:       req.GetName(),
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
	})
	if err != nil {
		slog.WarnContext(ctx, "token not found", "name", req.GetName())
		return nil, rpcerr.New(connect.CodeNotFound, ErrTokenNotFound)
	}

	return &tokenv1.GetTokenResponse{
		Token: apiTokenRowToProto(
			token.Name,
			token.EntityType,
			token.EntityID,
			token.Scopes,
			token.CreatedAt,
			token.ExpiresAt,
			token.LastUsedAt,
		),
	}, nil
}

// RevokeToken revokes/deletes an API token
func (s *TokenServer) RevokeToken(
	ctx context.Context,
	req *tokenv1.RevokeTokenRequest,
) (*tokenv1.RevokeTokenResponse, error) {
	if req.GetName() == "" {
		slog.ErrorContext(ctx, "invalid request: name is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, errNameRequired)
	}

	if req.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_UNSPECIFIED {
		slog.ErrorContext(ctx, "invalid request: entity_type is required")
		return nil, rpcerr.New(connect.CodeInvalidArgument, errEntityTypeRequired)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityID, err := uuid.Parse(req.GetEntityId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid entity id format", "entityId", req.GetEntityId())
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidEntityID).WithCause(err)
	}

	targetEntity := genDb.Entity{
		Type: protoEntityTypeToDb(req.GetEntityType()),
		ID:   entityID,
	}

	hasWritePermission := s.authz.Check(ctx, entityScopes, genDb.EntityScope{
		EntityType: targetEntity.Type,
		EntityID:   targetEntity.ID,
		Scope:      genDb.ScopeWrite,
	}) == nil

	isOwnToken := targetEntity.Type == genDb.EntityTypeUser && targetEntity.ID == entity.ID

	if !hasWritePermission && !isOwnToken {
		slog.WarnContext(
			ctx,
			"unauthorized to revoke token",
			"entityType",
			targetEntity.Type,
			"entityId",
			targetEntity.ID.String(),
			"user_id",
			entity.ID.String(),
		)
		return nil, connect.NewError(
			connect.CodePermissionDenied,
			"insufficient permissions to revoke token",
		)
	}

	if err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if deleteErr := qtx.DeleteAPITokenByNameAndEntity(ctx, genDb.DeleteAPITokenByNameAndEntityParams{
			Name:       req.GetName(),
			EntityType: targetEntity.Type,
			EntityID:   targetEntity.ID,
		}); deleteErr != nil {
			return deleteErr
		}
		return events.Record(ctx, qtx, tokenEvent(events.TokenRevoked, targetEntity, req.GetName()))
	}); err != nil {
		slog.ErrorContext(ctx, "failed to delete token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to revoke token")
	}

	slog.InfoContext(
		ctx,
		"revoked token",
		"name",
		req.GetName(),
		"entityType",
		targetEntity.Type,
		"entityId",
		targetEntity.ID.String(),
	)

	return &tokenv1.RevokeTokenResponse{}, nil
}

// GetScopes returns the entity and all scopes the current token has access to.
func (*TokenServer) GetScopes(
	ctx context.Context,
	_ *tokenv1.GetScopesRequest,
) (*tokenv1.GetScopesResponse, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	protoScopes := make([]*tokenv1.EntityScope, len(entityScopes))
	for i, es := range entityScopes {
		protoScopes[i] = &tokenv1.EntityScope{
			Scope:      dbScopeToProto(es.Scope),
			EntityType: dbEntityTypeToProto(es.EntityType),
			EntityId:   es.EntityID.String(),
		}
	}

	return &tokenv1.GetScopesResponse{
		EntityType: dbEntityTypeToProto(entity.Type),
		EntityId:   entity.ID.String(),
		Scopes:     protoScopes,
	}, nil
}

// CheckPermission checks the authenticated caller's scopes for the requested entity.
func (s *TokenServer) CheckPermission(
	ctx context.Context,
	req *tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	entityID, err := uuid.Parse(req.GetEntityId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidEntityID).WithCause(err)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	allowed := s.authz.Check(ctx, scopes, genDb.EntityScope{
		EntityType: protoEntityTypeToDb(req.GetEntityType()),
		EntityID:   entityID,
		Scope:      protoScopeToDb(req.GetScope()),
	}) == nil

	return &tokenv1.CheckPermissionResponse{Allowed: allowed}, nil
}

// Helper functions

func apiTokenRowToProto(
	name string,
	entityType genDb.EntityType,
	entityID uuid.UUID,
	dbScopes []genDb.EntityScope,
	createdAt time.Time,
	expiresAt time.Time,
	lastUsedAt *time.Time,
) *tokenv1.Token {
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
	case tokenv1.Scope_SCOPE_MEMBER:
		return genDb.ScopeMember
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
	case genDb.ScopeMember:
		return tokenv1.Scope_SCOPE_MEMBER
	default:
		return tokenv1.Scope_SCOPE_UNSPECIFIED
	}
}

func tokenEvent(eventType string, target genDb.Entity, name string) events.Event {
	ev := events.Event{
		Type:        eventType,
		SubjectType: events.SubjectToken,
		Data: map[string]any{
			events.FieldName: name,
			"entityType":     string(target.Type),
			"entityId":       target.ID.String(),
		},
	}
	switch target.Type {
	case genDb.EntityTypeOrganization:
		ev.OrgID = new(target.ID)
	case genDb.EntityTypeWorkspace:
		ev.WorkspaceID = new(target.ID)
	case genDb.EntityTypeResource:
		ev.ResourceID = new(target.ID)
	case genDb.EntityTypeUser:
	case genDb.EntityTypeSystem:
	}
	return ev
}
