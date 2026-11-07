package service

import (
	"context"
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
	return check(ctx, authorizer, p, actions.New(action, id.String()))
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
func check(ctx context.Context, authorizer *authz.Authorizer, p reqctx.Principal, required genDb.EntityScope) error {
	err := authorizer.Check(ctx, p.Scopes, required)
	if err == nil {
		return nil
	}
	if accessDenied(err) {
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	return fmt.Errorf("check access: %w", err)
}

// parseID parses a request field holding an entity ID.
func parseID(field, value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, "invalid "+field).WithCause(err)
	}
	return id, nil
}
