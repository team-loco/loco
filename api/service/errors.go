package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrImproperUsage = errors.New("improper usage of the api")
	ErrDB            = errors.New(http.StatusText(http.StatusInternalServerError))
	ErrUnauthorized  = errors.New("unauthorized")

	errEntityScopesNotFound     = errors.New("entity scopes not found in context")
	errCloneServiceSpec         = errors.New("failed to clone service spec")
	errCustomDomainsUnsupported = errors.New("custom domains are not supported")
)

func txError(ctx context.Context, msg string, err error) error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr
	}
	slog.ErrorContext(ctx, msg, "error", err)
	return connect.NewError(connect.CodeInternal, ErrDB)
}

// isPgConstraintViolation checks if an error is a PostgreSQL unique constraint violation
func isPgConstraintViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}

func isPgForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
