package service

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
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

func TestHandlerErrorMapsDomainErrorsToCodes(t *testing.T) {
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
		"wrapped precondition keeps its detail": {
			err:         fmt.Errorf("%w: %s", ErrOrgSuspended, "unpaid invoice"),
			wantCode:    connect.CodeFailedPrecondition,
			wantMessage: ErrOrgSuspended.Error() + ": unpaid invoice",
		},
		"invalid argument": {
			err:         fmt.Errorf("%w '%s'", errNoDeploymentInRegion, "eu-west-1"),
			wantCode:    connect.CodeInvalidArgument,
			wantMessage: "resource has no active deployment in region 'eu-west-1'",
		},
		"invalid input hides its cause": {
			err:         &invalidInputError{public: publicInvalidRequest, err: errImageRequired},
			wantCode:    connect.CodeInvalidArgument,
			wantMessage: publicInvalidRequest,
		},
		"anything else is a server failure": {
			err:         errors.New("connection reset"),
			wantCode:    connect.CodeInternal,
			wantMessage: ErrDB.Error(),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mapped := handlerError(t.Context(), "test", tc.err)
			connectErr, ok := errors.AsType[*connect.Error](mapped)
			if !ok {
				t.Fatalf("err = %v, want a connect error", mapped)
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
