package installation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const verificationStageCanary = "private-provider-credential-canary"

type rejectedVerificationAPI struct {
	buildv1connect.UnimplementedBuildServiceHandler
	observabilityv1connect.UnimplementedObservabilityProxyServiceHandler
}

func (*rejectedVerificationAPI) GetBuild(context.Context, *buildv1.GetBuildRequest) (*buildv1.GetBuildResponse, error) {
	return nil, connect.NewError(connect.CodeInternal, verificationStageCanary)
}

func (*rejectedVerificationAPI) QueryLogs(
	context.Context, *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	return nil, connect.NewError(connect.CodeInternal, verificationStageCanary)
}

func TestVerificationRPCFailuresIdentifyStageWithoutProviderOutput(t *testing.T) {
	server := connect.NewServer()
	api := &rejectedVerificationAPI{}
	buildv1connect.RegisterBuildServiceHandler(server, api)
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(server, api)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	defer endpoint.Close()
	clients := rpc.ForHost(endpoint.URL, "fixture-token")
	for _, tc := range []struct {
		name, want string
		run        func() error
	}{
		{"build", "verify source build: installation source verification failed", func() error {
			return verificationBuild(t.Context(), clients, "fixture-build")
		}},
		{"logs", "verify application logs: installation source verification failed", func() error {
			return verificationLogs(t.Context(), clients, &observabilityv1.QueryLogsRequest{}, "fixture-marker")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if !errors.Is(err, errVerification) || err.Error() != tc.want {
				t.Fatalf("missing fixed verification stage: %v", err)
			}
			if strings.Contains(err.Error(), verificationStageCanary) || strings.Contains(err.Error(), endpoint.URL) {
				t.Fatal("verification error exposed provider output")
			}
		})
	}
}

func TestVerificationHTTPSFailureIdentifiesStageWithoutResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, verificationStageCanary); err != nil {
			t.Error(err)
		}
		cancel()
	}))
	defer endpoint.Close()
	err := verificationProbe(ctx, endpoint.URL+"/"+verificationStageCanary, "private-expected-marker")
	if !errors.Is(err, errVerification) || err.Error() != "verify HTTPS: installation source verification failed" {
		t.Fatalf("missing fixed HTTPS stage: %v", err)
	}
	for _, private := range []string{verificationStageCanary, endpoint.URL, "private-expected-marker"} {
		if strings.Contains(err.Error(), private) {
			t.Fatal("verification error exposed response, URL or marker")
		}
	}
}
