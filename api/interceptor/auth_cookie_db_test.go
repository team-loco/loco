package interceptor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/api/auth"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
)

func TestCookieAuthenticationRequiresConnectAndTrustedOrigin(t *testing.T) {
	t.Parallel()
	const (
		webOrigin   = "https://app.example.test"
		corsOrigin  = "https://preview.example.test"
		otherOrigin = "https://other.example.test"
	)
	policy, err := auth.ParseSignupPolicy("open")
	if err != nil {
		t.Fatal(err)
	}
	f := newProviderFixture(t, policy)
	provider := f.issuer.Sign("k1", f.issuer.Claims("web-user", "web@example.test", true))
	const procedure = userv1connect.UserServiceWhoAmIProcedure
	ctx, err := f.interceptor.authenticate(t.Context(), procedure, withBearer(provider))
	if err != nil {
		t.Fatal(err)
	}
	principal, _ := reqctx.PrincipalFrom(ctx)
	identities, err := f.queries.ListIdentitiesForUser(ctx, principal.UserID)
	if err != nil || len(identities) != 1 {
		t.Fatalf("fixture identities = %v, error = %v", identities, err)
	}
	web, _, err := f.machine.IssueWebSession(ctx, principal.UserID, identities[0].ID, netip.Addr{}, "")
	if err != nil {
		t.Fatal(err)
	}
	cli, _, err := f.machine.IssueSession(ctx, principal.UserID, nil, netip.Addr{}, "")
	if err != nil {
		t.Fatal(err)
	}
	apiToken, _, err := f.machine.Issue(ctx, "api", principal.UserID, principal.Entity, principal.Scopes, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	f.interceptor.WebCookies(auth.NewWebCookies("https://APP.example.test:443", []string{corsOrigin}))
	server := connect.NewServer(f.interceptor.Interceptor())
	// This read-only test RPC supports both Connect GET and POST.
	method := userv1.File_loco_user_v1_user_proto.Services().ByName("UserService").Methods().ByName("WhoAmI")
	server.Register(connect.Method{
		Spec: connect.Spec{
			Procedure:        procedure,
			Schema:           method,
			StreamType:       connect.StreamTypeUnary,
			IdempotencyLevel: connect.IdempotencyNoSideEffects,
		},
		Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
			p, _ := reqctx.PrincipalFrom(ctx)
			if p.Token == web && (p.Method != genDb.DeploymentMethodWeb || p.IdentityID != identities[0].ID) {
				t.Errorf("dashboard method = %v, identity = %s", p.Method, p.IdentityID)
			}
			if err := stream.Receive(&userv1.WhoAmIRequest{}); err != nil {
				return err
			}
			return stream.Send(&userv1.WhoAmIResponse{})
		},
	})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)

	for _, tc := range []struct {
		name, method, origin, protocol, token, authorization string
		want                                                 int
	}{
		{"dashboard POST", http.MethodPost, webOrigin, "1", web, "", http.StatusOK},
		{"POST without Origin", http.MethodPost, "", "1", web, "", http.StatusOK},
		{"allowed sibling POST", http.MethodPost, corsOrigin, "1", web, "", http.StatusOK},
		{"other origin", http.MethodPost, otherOrigin, "1", web, "", http.StatusUnauthorized},
		{"missing protocol", http.MethodPost, webOrigin, "", web, "", http.StatusUnauthorized},
		{"GET with trusted origin", http.MethodGet, webOrigin, "1", web, "", http.StatusOK},
		{"GET without Origin", http.MethodGet, "", "1", web, "", http.StatusUnauthorized},
		{"CLI cookie", http.MethodPost, webOrigin, "1", cli, "", http.StatusUnauthorized},
		{"API token cookie", http.MethodPost, webOrigin, "1", apiToken, "", http.StatusUnauthorized},
		{"bearer takes precedence", http.MethodPost, otherOrigin, "", web, "Bearer " + cli, http.StatusOK},
		{"invalid bearer precedence", http.MethodPost, webOrigin, "1", web, "Basic invalid", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, body := procedure, "{}"
			if tc.method == http.MethodGet {
				path += "?connect=v1&encoding=json&message=%7B%7D"
				body = ""
			}
			req := httptest.NewRequestWithContext(t.Context(), tc.method, path, strings.NewReader(body))
			if tc.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Cookie", "__Host-loco_session="+tc.token)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.protocol != "" {
				req.Header.Set("Connect-Protocol-Version", tc.protocol)
			}
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			res := httptest.NewRecorder()
			mux.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", res.Code, tc.want, res.Body)
			}
		})
	}
}
