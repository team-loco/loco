package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
)

// lockUserMutation locks human actor and targets before any organization locks and
// reloads the actor's permissions inside the mutation transaction.
func lockUserMutation(ctx context.Context, qtx *genDb.Queries, targets ...uuid.UUID) (context.Context, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if p.UserID != uuid.Nil {
		targets = append(targets, p.UserID)
	}
	if lockErr := authz.LockUsers(ctx, qtx, targets...); errors.Is(lockErr, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	} else if lockErr != nil {
		return nil, fmt.Errorf("lock user accounts: %w", lockErr)
	}
	if p.UserID == uuid.Nil {
		return ctx, nil
	}
	user, err := qtx.GetUserByID(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("read locked user: %w", err)
	}
	if user.Status != genDb.UserStatusActive {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
	}
	authorizer := authz.New(qtx)
	live, err := authorizer.UserScopes(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("reload user permissions: %w", err)
	}
	if p.Method == genDb.DeploymentMethodApiToken {
		p.Scopes, err = authorizer.Intersect(ctx, p.Scopes, live)
		if err != nil {
			return nil, fmt.Errorf("bound token permissions: %w", err)
		}
	} else {
		p.Scopes = live
	}
	return reqctx.WithPrincipal(ctx, p), nil
}
