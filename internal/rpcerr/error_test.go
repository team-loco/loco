package rpcerr_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errPublicFailure = errors.New("public failure")

func TestNewPreservesCodeMessageAndWrappedCause(t *testing.T) {
	cause := fmt.Errorf("operation: %w", errPublicFailure)
	for _, code := range []connect.Code{connect.CodeInternal, connect.CodeInvalidArgument, connect.CodePermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			failure := rpcerr.New(code, cause)
			if failure.Code() != code {
				t.Fatalf("code = %s, want %s", failure.Code(), code)
			}
			if failure.Message() != "operation: public failure" {
				t.Fatalf("message = %q", failure.Message())
			}
			if !errors.Is(failure, errPublicFailure) {
				t.Fatal("wrapped cause was lost")
			}
		})
	}
}

type failureServer struct {
	configv1connect.UnimplementedConfigServiceHandler
}

func (*failureServer) GetConfig(context.Context, *configv1.GetConfigRequest) (*configv1.GetConfigResponse, error) {
	return nil, rpcerr.New(connect.CodeInternal, errPublicFailure)
}

func TestNewPreservesClientVisibleError(t *testing.T) {
	server := connect.NewServer()
	configv1connect.RegisterConfigServiceHandler(server, &failureServer{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	transport := connecthttp.NewTransport(httpServer.Client(), httpServer.URL)
	connectClient := connect.NewClient(transport)
	configClient := configv1connect.NewConfigServiceClient(connectClient)
	_, err := configClient.GetConfig(t.Context(), &configv1.GetConfigRequest{})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %s (%v)", connect.CodeOf(err), err)
	}
	failure, ok := errors.AsType[*connect.Error](err)
	if !ok || failure.Message() != "public failure" {
		t.Fatalf("client error = %v", err)
	}
	if errors.Is(err, errPublicFailure) {
		t.Fatal("server cause reached the client")
	}
}

var errExpected = rpcerr.NewCoded(connect.CodeNotFound, "thing not found")

func TestPublicReturnsOnlyTheCodedMessage(t *testing.T) {
	cases := map[string]struct {
		err         error
		wantMessage string
	}{
		"wrapped": {
			err:         fmt.Errorf("load thing 0190: %w", errExpected),
			wantMessage: "thing not found",
		},
		"detail": {
			err:         fmt.Errorf("load: %w", errExpected.Detail("%s", "web")),
			wantMessage: "thing not found: web",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			public, ok := rpcerr.Public(tc.err)
			if !ok {
				t.Fatal("no coded error found")
			}
			if public.Code() != connect.CodeNotFound || public.Message() != tc.wantMessage {
				t.Fatalf("public = %s %q, want not_found %q", public.Code(), public.Message(), tc.wantMessage)
			}
			if !errors.Is(public, errExpected) {
				t.Fatal("the cause no longer matches the sentinel")
			}
		})
	}
	if public, ok := rpcerr.Public(errors.New("disk full")); ok {
		t.Fatalf("an uncoded error was treated as public: %v", public)
	}
}
