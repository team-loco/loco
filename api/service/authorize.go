package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
)

// authorize checks that the caller holds action on the entity id. A caller without a principal
// gets Unauthenticated and one whose scopes do not cover the action gets PermissionDenied.
func authorize(ctx context.Context, authorizer *authz.Authorizer, action actions.Action, id uuid.UUID) error {
	p, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	return check(ctx, authorizer, p, actions.New(action, id))
}

// authorizeSystem is authorize for an action on the installation rather than one entity.
func authorizeSystem(ctx context.Context, authorizer *authz.Authorizer, action actions.Action) error {
	p, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	return check(ctx, authorizer, p, actions.NewSystem(action))
}

// requireSession returns the caller when it signed in with a session, and PermissionDenied for
// an API token.
func requireSession(ctx context.Context) (reqctx.Principal, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return reqctx.Principal{}, err
	}
	if p.SessionID == uuid.Nil {
		return reqctx.Principal{}, connect.NewError(connect.CodePermissionDenied, publicSessionRequired)
	}
	return p, nil
}

// grantedResourceIDs returns the resources the scopes name directly, with any scope.
func grantedResourceIDs(scopes []genDb.EntityScope) []uuid.UUID {
	var ids []uuid.UUID
	for _, scope := range scopes {
		if scope.EntityType == genDb.EntityTypeResource {
			ids = append(ids, scope.EntityID)
		}
	}
	return ids
}

// principalFrom returns the caller the auth interceptor attached to ctx.
func principalFrom(ctx context.Context) (reqctx.Principal, error) {
	p, ok := reqctx.PrincipalFrom(ctx)
	if !ok {
		return reqctx.Principal{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
	}
	return p, nil
}

// check is authorize for a handler that already holds the principal.
func check(ctx context.Context, authorizer *authz.Authorizer, p reqctx.Principal, required ...genDb.EntityScope) error {
	err := authorizer.Check(ctx, p.Scopes, required...)
	if err == nil {
		markOperator(ctx, authorizer, p, required)
		return nil
	}
	if accessDenied(err) {
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	return fmt.Errorf("check access: %w", err)
}

// markOperator marks the request when the caller's installation scopes were needed to pass the check.
func markOperator(ctx context.Context, authorizer *authz.Authorizer, p reqctx.Principal, required []genDb.EntityScope) {
	var own []genDb.EntityScope
	for _, scope := range p.Scopes {
		if scope.EntityType != genDb.EntityTypeSystem {
			own = append(own, scope)
		}
	}
	if len(own) == len(p.Scopes) {
		return
	}
	if errors.Is(authorizer.Check(ctx, own, required...), authz.ErrInsufficientPermissions) {
		reqctx.MarkOperator(ctx)
	}
}

// parseIDs parses a repeated request field holding entity IDs; it returns nil for an empty field.
func parseIDs(field string, values []string) ([]uuid.UUID, error) {
	if len(values) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		id, err := parseID(field, value)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// parseID parses a request field holding an entity ID.
func parseID(field, value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, "invalid "+field).WithCause(err)
	}
	return id, nil
}
