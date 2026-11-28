package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
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
	p, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	err = authorizer.Check(ctx, p.Scopes, required)
	if err == nil {
		return nil
	}
	if !accessDenied(err) {
		return fmt.Errorf("check access: %w", err)
	}
	for _, scope := range visibleScopes {
		visible := required
		visible.Scope = scope
		visibleErr := authorizer.Check(ctx, p.Scopes, visible)
		if visibleErr == nil {
			return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
		}
		if !accessDenied(visibleErr) {
			return fmt.Errorf("check %s access: %w", scope, visibleErr)
		}
	}
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
	required := actions.New(action, env.WorkspaceID)
	if authErr := authorizeVisible(ctx, authorizer, required, ErrEnvironmentNotFound); authErr != nil {
		return genDb.Environment{}, authErr
	}
	return env, nil
}

func accessDenied(err error) bool {
	return errors.Is(err, authz.ErrInsufficientPermissions) || errors.Is(err, authz.ErrEntityNotFound)
}

// readableWorkspaceResources is nil for a caller who can list the workspace's resources, else the
// resources of the workspace GetResource would return to the caller. A caller who can read none of
// them is denied. Without workspace read, only a grant on the resource itself passes GetResource's
// check, so only the caller's resource grants are checked.
func readableWorkspaceResources(
	ctx context.Context,
	authorizer *authz.Authorizer,
	queries genDb.Querier,
	wsID uuid.UUID,
) ([]uuid.UUID, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	denied := check(ctx, authorizer, p, actions.New(actions.ListResources, wsID))
	if denied == nil || connect.CodeOf(denied) != connect.CodePermissionDenied {
		return nil, denied
	}
	granted, err := grantedReadableResources(ctx, authorizer, p)
	if err != nil {
		return nil, err
	}
	if len(granted) == 0 {
		return nil, denied
	}
	rows, err := queries.ListResourcesByIDs(ctx, granted)
	if err != nil {
		return nil, fmt.Errorf("list the granted resources: %w", err)
	}
	var readable []uuid.UUID
	for _, row := range rows {
		if row.WorkspaceID == wsID {
			readable = append(readable, row.ID)
		}
	}
	if len(readable) == 0 {
		return nil, denied
	}
	return readable, nil
}

// grantedReadableResources are the resources granted to the caller directly that GetResource lets it read.
func grantedReadableResources(
	ctx context.Context,
	authorizer *authz.Authorizer,
	p reqctx.Principal,
) ([]uuid.UUID, error) {
	var granted []uuid.UUID
	for _, id := range grantedResourceIDs(p.Scopes) {
		checkErr := check(ctx, authorizer, p, actions.New(actions.GetResource, id))
		if checkErr == nil {
			granted = append(granted, id)
		} else if connect.CodeOf(checkErr) != connect.CodePermissionDenied {
			return nil, checkErr
		}
	}
	return granted, nil
}
