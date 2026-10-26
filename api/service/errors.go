package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrImproperUsage = errors.New("improper usage of the api")
	ErrDB            = errors.New(http.StatusText(http.StatusInternalServerError))
	ErrUnauthorized  = errors.New("unauthorized")

	errEntityScopesNotFound = errors.New("entity scopes not found in context")
	errUnknownOperation     = errors.New("configplan produced an operation kind outside KindCreate..KindImport")
)

func txError(ctx context.Context, msg string, err error) error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr
	}
	slog.ErrorContext(ctx, msg, "error", err)
	return rpcerr.New(connect.CodeInternal, ErrDB)
}

// isPgConstraintViolation checks if an error is a PostgreSQL unique constraint violation
func isPgConstraintViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}

const (
	pgResourceWorkspaceReference = "resources_workspace_id_fkey"
	pgWorkspaceOrgReference      = "workspaces_org_id_fkey"
	pgOrgScopeReference          = "user_scopes_organization_ref_fkey"
	pgWorkspaceScopeReference    = "user_scopes_workspace_ref_fkey"
	pgUserScopeOwnerReference    = "user_scopes_user_id_fkey"
)

func pgForeignKeyConstraint(err error) string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "23503" {
		return ""
	}
	return pgErr.ConstraintName
}

const pgLockNotAvailable = "55P03"

func isPgLockNotAvailable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgLockNotAvailable
}
