package service

import (
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrImproperUsage = errors.New("improper usage of the api")

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

func withDetail(err *connect.Error, msg proto.Message) *connect.Error {
	detail, detailErr := connectproto.NewErrorDetail(msg)
	if detailErr != nil {
		slog.Error("failed to attach an error detail", "detail", proto.MessageName(msg), "error", detailErr)
		return err
	}
	return err.WithDetail(detail)
}

const (
	buildErrorDomain               = "loco.build"
	imageNotFoundReason            = "IMAGE_NOT_FOUND"
	imageAccessDeniedReason        = "IMAGE_ACCESS_DENIED"
	imageReferenceInvalidReason    = "IMAGE_REFERENCE_INVALID"
	imagePlatformUnsupportedReason = "IMAGE_PLATFORM_UNSUPPORTED"
)

func fieldError(code connect.Code, field, message, reason string) *connect.Error {
	err := withDetail(connect.NewError(code, message), &errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: field, Description: message}},
	})
	if reason != "" {
		return withDetail(err, &errdetails.ErrorInfo{Reason: reason, Domain: buildErrorDomain})
	}
	return err
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
	pgWorkspaceScopeReference = "user_scopes_workspace_ref_fkey"
)

func pgForeignKeyConstraint(err error) string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || (pgErr.Code != "23503" && pgErr.Code != "23001") {
		return ""
	}
	return pgErr.ConstraintName
}

const pgLockNotAvailable = "55P03"

func isPgLockNotAvailable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgLockNotAvailable
}
