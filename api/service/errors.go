package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrImproperUsage = errors.New("improper usage of the api")
	ErrDB            = errors.New(http.StatusText(http.StatusInternalServerError))

	errUnknownOperation = errors.New("configplan produced an operation kind outside KindCreate..KindImport")
)

// invalidInputError rejects a request with a fixed public message. err stays the private cause.
type invalidInputError struct {
	public string
	err    error
}

func (e *invalidInputError) Error() string {
	return e.err.Error()
}

// Unwrap puts the public message first, so rpcerr.Public reports it rather than a coded error
// inside the private cause.
func (e *invalidInputError) Unwrap() []error {
	return []error{rpcerr.NewCoded(connect.CodeInvalidArgument, e.public), e.err}
}

// handlerError returns the public error of a coded failure. It logs any other error with msg
// and attrs and hides it behind ErrDB.
func handlerError(ctx context.Context, msg string, err error, attrs ...any) error {
	if public, ok := rpcerr.Public(err); ok {
		return public
	}
	attrs = append(attrs, "error", err)
	slog.ErrorContext(ctx, msg, attrs...)
	return rpcerr.New(connect.CodeInternal, ErrDB)
}

// txError maps the error of a transaction whose closure may still return connect errors.
func txError(ctx context.Context, msg string, err error) error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr
	}
	return handlerError(ctx, msg, err)
}

func withDetail(err *connect.Error, msg proto.Message) *connect.Error {
	detail, detailErr := connectproto.NewErrorDetail(msg)
	if detailErr != nil {
		slog.Error("failed to attach an error detail", "detail", proto.MessageName(msg), "error", detailErr)
		return err
	}
	return err.WithDetail(detail)
}

// Unique constraints whose violation is a client error. Postgres names an inline UNIQUE
// <table>_<columns>_key; the rest are the index names in api/migrations.
const (
	pgEnvironmentNameUnique   = "environments_workspace_id_name_key"
	pgResourceNameUnique      = "resources_workspace_id_name_key"
	pgResourceSlugUnique      = "resources_workspace_id_slug_key"
	pgDomainUnique            = "resource_domains_domain_key"
	pgPlatformSubdomainUnique = "uniq_platform_subdomain"
	pgOpenInvitationUnique    = "invitations_open_idx"
	pgActiveDeploymentUnique  = "uniq_deployments_resource_region_active"
	pgClusterKeyUnique        = "clusters_cluster_key_key"
)

func isUniqueViolation(err error, constraint string) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

// isDomainTaken reports whether a resource_domains write hit another resource's hostname.
func isDomainTaken(err error) bool {
	return isUniqueViolation(err, pgDomainUnique) || isUniqueViolation(err, pgPlatformSubdomainUnique)
}

const (
	pgResourceWorkspaceReference = "resources_workspace_id_fkey"
	pgWorkspaceOrgReference      = "workspaces_org_id_fkey"
	pgWorkspaceScopeReference    = "user_scopes_workspace_ref_fkey"
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
