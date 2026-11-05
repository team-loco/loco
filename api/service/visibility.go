package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/rpcerr"
)

// authorizeVisible checks required against the caller's scopes. A caller who cannot read the
// entity gets notFound, the answer for an ID that does not exist, so the two cases look the same
// to them. A caller who can read the entity but lacks required gets PermissionDenied.
func authorizeVisible(
	ctx context.Context,
	authorizer *authz.Authorizer,
	required genDb.EntityScope,
	notFound error,
) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}
	err := authorizer.Check(ctx, scopes, required)
	if err == nil {
		return nil
	}
	if !accessDenied(err) {
		return fmt.Errorf("check access: %w", err)
	}
	for _, scope := range visibleScopes {
		visible := required
		visible.Scope = scope
		visibleErr := authorizer.Check(ctx, scopes, visible)
		if visibleErr == nil {
			slog.WarnContext(ctx, "caller lacks the scope", "entityId", required.EntityID, "scope", required.Scope)
			return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
		}
		if !accessDenied(visibleErr) {
			return fmt.Errorf("check %s access: %w", scope, visibleErr)
		}
	}
	slog.WarnContext(ctx, "caller cannot see the entity", "entityId", required.EntityID)
	return rpcerr.New(connect.CodeNotFound, notFound)
}

// visibleScopes are the scopes that let a caller see an entity: read, or membership, which is
// all an organization member holds.
var visibleScopes = []genDb.Scope{genDb.ScopeRead, genDb.ScopeMember}

// visibleEnvironment loads an environment and authorizes action on its workspace, answering a
// caller who cannot read the workspace as if the environment did not exist.
func visibleEnvironment(
	ctx context.Context,
	queries genDb.Querier,
	authorizer *authz.Authorizer,
	environmentID uuid.UUID,
	action actions.Action,
) (genDb.Environment, error) {
	env, err := queries.GetEnvironmentByID(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Environment{}, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	if err != nil {
		return genDb.Environment{}, fmt.Errorf("get environment %s: %w", environmentID, err)
	}
	required := actions.New(action, env.WorkspaceID.String())
	if authErr := authorizeVisible(ctx, authorizer, required, ErrEnvironmentNotFound); authErr != nil {
		return genDb.Environment{}, authErr
	}
	return env, nil
}

func accessDenied(err error) bool {
	return errors.Is(err, authz.ErrInsufficientPermissions) || errors.Is(err, authz.ErrEntityNotFound)
}
