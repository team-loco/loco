package interceptor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errPrivateFailure = errors.New("private handler diagnostic")

var errThingNotFound = rpcerr.NewCoded(connect.CodeNotFound, "thing not found")

type failingConfig struct {
	configv1connect.UnimplementedConfigServiceHandler
	err error
}

func (c *failingConfig) GetConfig(context.Context, *configv1.GetConfigRequest) (*configv1.GetConfigResponse, error) {
	return nil, c.err
}

func TestHandlerErrorsOnTheWire(t *testing.T) {
	cases := map[string]struct {
		err         error
		wantCode    connect.Code
		wantMessage string
		wantLogs    int
	}{
		"plain failure": {
			err:         fmt.Errorf("load thing 0190: %w", errPrivateFailure),
			wantCode:    connect.CodeInternal,
			wantMessage: publicRequestFailed,
			wantLogs:    1,
		},
		"wrapped coded error": {
			err:         fmt.Errorf("load thing 0190: %w", errThingNotFound),
			wantCode:    connect.CodeNotFound,
			wantMessage: errThingNotFound.Error(),
		},
		"connect error built by the handler": {
			err:         connect.NewError(connect.CodePermissionDenied, "denied"),
			wantCode:    connect.CodePermissionDenied,
			wantMessage: "denied",
		},
		"wrapped connect error": {
			err: fmt.Errorf(
				"call upstream: %w",
				connect.NewError(connect.CodePermissionDenied, "upstream detail"),
			),
			wantCode:    connect.CodeInternal,
			wantMessage: publicRequestFailed,
			wantLogs:    1,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			logs := captureLogs(t)
			server := connect.NewServer(NewErrorInterceptor())
			configv1connect.RegisterConfigServiceHandler(server, &failingConfig{err: tc.err})
			client := configv1connect.NewConfigServiceClient(connect.NewClient(connectinprocess.New(server)))
			_, err := client.GetConfig(t.Context(), &configv1.GetConfigRequest{})
			failure, ok := errors.AsType[*connect.Error](err)
			if !ok || failure.Code() != tc.wantCode || failure.Message() != tc.wantMessage {
				t.Fatalf("wire error = %v, want %s %q", err, tc.wantCode, tc.wantMessage)
			}
			if got := strings.Count(logs.String(), "level=ERROR"); got != tc.wantLogs {
				t.Fatalf("error records = %d, want %d: %s", got, tc.wantLogs, logs)
			}
		})
	}
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

func TestPublicHandlerErrorsKeepServerCause(t *testing.T) {
	for _, cause := range []error{errPrivateFailure, context.Canceled, context.DeadlineExceeded} {
		wrapped := NewErrorInterceptor()(
			func(context.Context, connect.Spec, connect.ServerStream) error { return cause },
		)
		err := wrapped(t.Context(), connect.Spec{}, nil)
		if !errors.Is(err, cause) {
			t.Fatalf("error = %v, cause = %v", err, cause)
		}
	}
}
