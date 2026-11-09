package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/team-loco/loco/api/interceptor"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

func TestApplyRefusedLogsADetailThatCannotBeEncoded(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	invalidUTF8 := string([]byte{0xff})
	refusal := &planv1.ApplyRefusal{Images: map[string]string{"web": invalidUTF8}}
	err := applyRefused(errors.New("refused"), refusal)

	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("err = %v, want a connect error", err)
	}
	if connectErr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want %v", connectErr.Code(), connect.CodeFailedPrecondition)
	}
	if len(connectErr.Details()) != 0 {
		t.Fatalf("details = %v, want none", connectErr.Details())
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "invalid UTF-8") {
		t.Fatalf("logs = %q, want an ERROR line with the encoding failure", logs.String())
	}
}

func TestErrorInterceptorMapsDomainErrorsToCodes(t *testing.T) {
	cases := map[string]struct {
		err         error
		wantCode    connect.Code
		wantMessage string
	}{
		"not found": {
			err:         ErrBuildNotFound,
			wantCode:    connect.CodeNotFound,
			wantMessage: ErrBuildNotFound.Error(),
		},
		"wrapped precondition keeps its public detail only": {
			err:         fmt.Errorf("lock org 0190: %w", ErrOrgSuspended.Detail("%s", "unpaid invoice")),
			wantCode:    connect.CodeFailedPrecondition,
			wantMessage: ErrOrgSuspended.Error() + ": unpaid invoice",
		},
		"invalid argument": {
			err:         errNoDeploymentInRegion.Detail("%s", "eu-west-1"),
			wantCode:    connect.CodeInvalidArgument,
			wantMessage: "resource has no active deployment in region: eu-west-1",
		},
		"invalid input hides its cause": {
			err:         &invalidInputError{public: publicInvalidRequest, err: errImageRequired},
			wantCode:    connect.CodeInvalidArgument,
			wantMessage: publicInvalidRequest,
		},
		"invalid spec under a desired spec failure": {
			err:         fmt.Errorf("%w: %w", errDesiredSpec, &invalidSpecError{err: errors.New("port 80")}),
			wantCode:    connect.CodeInvalidArgument,
			wantMessage: "invalid deployment spec: port 80",
		},
		"anything else is a server failure": {
			err:         errors.New("connection reset"),
			wantCode:    connect.CodeInternal,
			wantMessage: "request failed",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			connectErr, ok := errors.AsType[*connect.Error](wireError(fmt.Errorf("handler: %w", tc.err)))
			if !ok {
				t.Fatalf("err = %v, want a connect error", tc.err)
			}
			if connectErr.Code() != tc.wantCode {
				t.Fatalf("code = %v, want %v", connectErr.Code(), tc.wantCode)
			}
			if connectErr.Message() != tc.wantMessage {
				t.Fatalf("message = %q, want %q", connectErr.Message(), tc.wantMessage)
			}
		})
	}
}

// wantServerFailure checks that the error interceptor turns err into Internal.
func wantServerFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want a server failure")
	}
	if code := wireCode(err); code != connect.CodeInternal {
		t.Fatalf("err = %v (%v), want a server failure", err, code)
	}
}

func TestUniqueViolationMatchesOnlyItsConstraint(t *testing.T) {
	violation := func(constraint string) error {
		return fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505", ConstraintName: constraint})
	}
	if !isUniqueViolation(violation(pgResourceNameUnique), pgResourceNameUnique) {
		t.Fatal("the named constraint was not matched")
	}
	if isUniqueViolation(violation(pgResourceSlugUnique), pgResourceNameUnique) {
		t.Fatal("a violation of another constraint matched")
	}
	if !isDomainTaken(violation(pgPlatformSubdomainUnique)) {
		t.Fatal("a taken platform subdomain is not a taken domain")
	}
	if isDomainTaken(violation("uniq_resource_primary_domain")) {
		t.Fatal("a second primary domain was reported as a taken domain")
	}
	foreignKey := &pgconn.PgError{Code: "23503", ConstraintName: pgResourceNameUnique}
	if isUniqueViolation(foreignKey, pgResourceNameUnique) {
		t.Fatal("a foreign key violation matched")
	}
}

// wireError is err as the error interceptor returns it to the client.
func wireError(err error) error {
	if err == nil {
		return nil
	}
	handler := interceptor.NewErrorInterceptor()(
		func(context.Context, connect.Spec, connect.ServerStream) error { return err },
	)
	return handler(context.Background(), connect.Spec{}, nil)
}

func wireCode(err error) connect.Code {
	return connect.CodeOf(wireError(err))
}

// publicError is the connect error a client sees for err, whether err came back from a client
// call or straight from a handler.
func publicError(err error) *connect.Error {
	if connectErr, ok := errors.AsType[*connect.Error](err); ok {
		return connectErr
	}
	if public, ok := rpcerr.Public(err); ok {
		return public
	}
	return nil
}
