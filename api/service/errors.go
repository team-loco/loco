package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrImproperUsage = errors.New("improper usage of the api")
	ErrDB            = errors.New(http.StatusText(http.StatusInternalServerError))

	errUnknownOperation = errors.New("configplan produced an operation kind outside KindCreate..KindImport")
)

// domainErrorCodes are the codes of the domain errors helpers return. The client sees the
// error's own message.
var domainErrorCodes = []struct {
	err  error
	code connect.Code
}{
	{ErrBuildNotFound, connect.CodeNotFound},
	{ErrDomainNotFound, connect.CodeNotFound},
	{ErrEnvironmentNotFound, connect.CodeNotFound},
	{ErrUserNotFound, connect.CodeNotFound},
	{ErrWorkspaceNotFound, connect.CodeNotFound},
	{ErrOrgNotFound, connect.CodeNotFound},
	{ErrResourceNotFound, connect.CodeNotFound},
	{errNoActiveDeployment, connect.CodeNotFound},
	{errBuildSourceRequired, connect.CodeInvalidArgument},
	{errDockerfileNoBuildID, connect.CodeInvalidArgument},
	{errBuildImageMismatch, connect.CodeInvalidArgument},
	{errImageBuildIDSet, connect.CodeInvalidArgument},
	{errImageRequired, connect.CodeInvalidArgument},
	{errImageFromLocoRegistry, connect.CodeInvalidArgument},
	{errNoDeploymentInRegion, connect.CodeInvalidArgument},
	{errPreviousDeploymentNoSpec, connect.CodeInvalidArgument},
	{errOnlyServiceResources, connect.CodeInvalidArgument},
	{errScaleUnchanged, connect.CodeInvalidArgument},
	{configplan.ErrCustomDomain, connect.CodeInvalidArgument},
	{configplan.ErrNotSingleLabel, connect.CodeInvalidArgument},
	{ErrAlreadyOrgMember, connect.CodeAlreadyExists},
	{ErrDomainAlreadyExists, connect.CodeAlreadyExists},
	{ErrEnvironmentNameNotUnique, connect.CodeAlreadyExists},
	{ErrInvitationExists, connect.CodeAlreadyExists},
	{ErrResourceNameNotUnique, connect.CodeAlreadyExists},
	{errDomainInUse, connect.CodeAlreadyExists},
	{errSlugInUse, connect.CodeAlreadyExists},
	{errBuildNotSucceeded, connect.CodeFailedPrecondition},
	{errBuildImageDeleted, connect.CodeFailedPrecondition},
	{errBuildNotQueueable, connect.CodeFailedPrecondition},
	{errBuildServiceNotCreated, connect.CodeFailedPrecondition},
	{errRolloutInProgress, connect.CodeFailedPrecondition},
	{ErrOrgSuspended, connect.CodeFailedPrecondition},
	{errNoProductionEnvironment, connect.CodeFailedPrecondition},
	{errNoActiveCluster, connect.CodeFailedPrecondition},
	{ErrClusterNotAssignable, connect.CodeFailedPrecondition},
	{configplan.ErrDomainNotServed, connect.CodeFailedPrecondition},
	{ErrCannotRemovePrimary, connect.CodeFailedPrecondition},
	{ErrEnvironmentInUse, connect.CodeFailedPrecondition},
	{ErrInvitationNotOpen, connect.CodeFailedPrecondition},
	{ErrNotOrgMember, connect.CodeFailedPrecondition},
	{ErrOrgHasWorkspacesWithResources, connect.CodeFailedPrecondition},
	{ErrWorkspaceHasResources, connect.CodeFailedPrecondition},
}

// invalidInputError rejects a request with a fixed public message. err stays the private cause.
type invalidInputError struct {
	public string
	err    error
}

func (e *invalidInputError) Error() string {
	return e.err.Error()
}

func (e *invalidInputError) Unwrap() error {
	return e.err
}

// mapError maps a domain error to the connect error a handler returns. Any other error is a
// server failure: it is wrapped with msg for the error interceptor to log and hide.
func mapError(msg string, err error) error {
	if mapped, ok := domainError(err); ok {
		return mapped
	}
	return fmt.Errorf("%s: %w", msg, err)
}

// handlerError maps a domain error like mapError, but logs any other error with msg and attrs
// itself and hides it behind ErrDB.
func handlerError(ctx context.Context, msg string, err error, attrs ...any) error {
	if mapped, ok := domainError(err); ok {
		return mapped
	}
	attrs = append(attrs, "error", err)
	slog.ErrorContext(ctx, msg, attrs...)
	return rpcerr.New(connect.CodeInternal, ErrDB)
}

func domainError(err error) (*connect.Error, bool) {
	if invalid, ok := errors.AsType[*invalidInputError](err); ok {
		return connect.NewError(connect.CodeInvalidArgument, invalid.public).WithCause(invalid.err), true
	}
	for _, domain := range domainErrorCodes {
		if errors.Is(err, domain.err) {
			return rpcerr.New(domain.code, err), true
		}
	}
	return nil, false
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

// isPgConstraintViolation checks if an error is a PostgreSQL unique constraint violation
func isPgConstraintViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
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
