package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	dashboardOrigin = "https://app.loco.test"
	dashboardEmail  = "dashboard@example.test"
	dashboardClient = "dashboard"
)

type webAuthFixture struct {
	*authFixture
	client authv1connect.AuthServiceClient
	OIDC   *webTestProvider
}

type webTestProvider struct {
	nonce, challenge string
	mu               sync.Mutex
}

func newWebAuthFixture(t *testing.T) *webAuthFixture {
	t.Helper()
	fixture := newAuthFixture(t)
	out := &webAuthFixture{authFixture: fixture, OIDC: &webTestProvider{}}

	signing := authtest.NewIssuer(t)
	var issuerURL string
	issuer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var response map[string]string
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			response = map[string]string{
				"issuer": issuerURL, "authorization_endpoint": issuerURL + "/authorize",
				"token_endpoint": issuerURL + "/token", "jwks_uri": signing.URL() + "/keys",
			}
		case "/token":
			out.OIDC.mu.Lock()
			defer out.OIDC.mu.Unlock()
			if err := r.ParseForm(); err != nil || r.Method != http.MethodPost ||
				r.Header.Get("Origin") != "" ||
				r.Form.Get("client_id") != dashboardClient ||
				r.Form.Get("redirect_uri") != dashboardOrigin+"/auth/callback" ||
				r.Form.Get("grant_type") != "authorization_code" ||
				challengeFor(r.Form.Get("code_verifier")) != out.OIDC.challenge {
				t.Error("unexpected provider exchange request")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			claims := signing.Claims("dashboard-user", dashboardEmail, true)
			claims["iss"] = issuerURL
			claims["nonce"] = out.OIDC.nonce
			if r.Form.Get("code") == "bad-nonce" {
				claims["nonce"] = "wrong"
			}
			response = map[string]string{
				"access_token": "provider-access", "id_token": signing.Sign("k1", claims),
				"token_type": "Bearer",
			}
		default:
			http.NotFound(w, r)
			return
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode provider response: %v", err)
		}
	}))
	issuerURL = "http://" + issuer.Listener.Addr().String()
	issuer.Start()
	t.Cleanup(issuer.Close)

	issuers, err := auth.ParseIssuers(
		`[{"issuer":"` + issuerURL + `","audience":"authenticated",` +
			`"web":{"clientId":"dashboard","scopes":"openid email offline_access"}}]`,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier := auth.NewVerifier(issuer.Client(), issuers)
	fixture.server.web = WebAuth{
		Provider: auth.NewWebProvider(issuer.Client(), verifier, issuers[0], dashboardOrigin),
		Resolver: openResolver(t, fixture.server.db),
		Cookies:  auth.NewWebCookies(dashboardOrigin, nil),
	}

	rpcServer := connect.NewServer()
	authv1connect.RegisterAuthServiceHandler(rpcServer, fixture.server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	out.client = authv1connect.NewAuthServiceClient(
		connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)),
	)
	return out
}

func webCall(t *testing.T, cookies ...*http.Cookie) (context.Context, *connect.CallInfo) {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Origin", dashboardOrigin)
	for _, cookie := range cookies {
		info.RequestHeader().Add("Cookie", cookie.Name+"="+cookie.Value)
	}
	return ctx, info
}

func responseCookie(t *testing.T, info *connect.CallInfo, suffix string) *http.Cookie {
	t.Helper()
	for _, value := range info.ResponseHeader().Values("Set-Cookie") {
		cookie, err := http.ParseSetCookie(value)
		if err != nil {
			t.Fatal(err)
		}
		if cookie.Name == "__Host-"+suffix {
			if !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" {
				t.Fatalf("incorrect cookie attributes for %s", cookie.Name)
			}
			return cookie
		}
	}
	t.Fatalf("missing %s cookie", suffix)
	return nil
}

func (f *webAuthFixture) start(t *testing.T, code string) (*authv1.CompleteWebLoginRequest, *http.Cookie) {
	t.Helper()
	ctx, info := webCall(t)
	started, err := f.client.StartWebLogin(
		ctx,
		&authv1.StartWebLoginRequest{RedirectTo: "/workspaces?tab=all", LoginHint: dashboardEmail},
	)
	if err != nil {
		t.Fatalf("start web login: %v", err)
	}
	authorization, err := url.Parse(started.GetAuthorizationUrl())
	if err != nil {
		t.Fatal(err)
	}
	values := authorization.Query()
	if values.Get("code_challenge_method") != "S256" || values.Get("code_challenge") == "" ||
		values.Get("nonce") == "" || values.Get(webLoginState) == "" ||
		values.Get("login_hint") != dashboardEmail || values.Get("scope") != "openid email" {
		t.Fatal("incorrect authorization parameters")
	}
	f.OIDC.mu.Lock()
	f.OIDC.challenge, f.OIDC.nonce = values.Get("code_challenge"), values.Get("nonce")
	f.OIDC.mu.Unlock()
	callback := url.Values{"code": {code}, webLoginState: {values.Get(webLoginState)}}
	return &authv1.CompleteWebLoginRequest{CallbackUrl: dashboardOrigin + "/auth/callback?" + callback.Encode()},
		responseCookie(t, info, auth.WebLoginCookie)
}

func TestWebLoginRequiresBoundSingleUseStateAndNonce(t *testing.T) {
	t.Parallel()
	f := newWebAuthFixture(t)

	callback, binding := f.start(t, "good-code")
	ctx, _ := webCall(t)
	if _, err := f.client.CompleteWebLogin(ctx, callback); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("callback without binding: %v", err)
	}

	bindingValue := binding.Value
	binding.Value = "wrong-browser"
	ctx, _ = webCall(t, binding)
	binding.Value = bindingValue
	if _, err := f.client.CompleteWebLogin(ctx, callback); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("callback with wrong binding: %v", err)
	}

	ctx, info := webCall(t, binding)
	signedIn, err := f.client.CompleteWebLogin(ctx, callback)
	if err != nil {
		t.Fatalf("complete web login: %v", err)
	}
	if signedIn.GetUser().GetEmail() != dashboardEmail || signedIn.GetRedirectTo() != "/workspaces?tab=all" {
		t.Fatalf("unexpected login result: %v", signedIn)
	}
	if cleared := responseCookie(t, info, auth.WebLoginCookie); cleared.MaxAge >= 0 {
		t.Fatal("expected completed login to clear its binding cookie")
	}
	access, refresh := responseCookie(t, info, auth.WebSessionCookie), responseCookie(t, info, auth.WebRefreshCookie)
	if access.SameSite != http.SameSiteLaxMode || refresh.SameSite != http.SameSiteStrictMode {
		t.Fatal("incorrect SameSite attributes")
	}

	ctx, _ = webCall(t, binding)
	if _, err := f.client.CompleteWebLogin(ctx, callback); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("callback replay: %v", err)
	}
	callback, binding = f.start(t, "bad-nonce")
	ctx, _ = webCall(t, binding)
	if _, err := f.client.CompleteWebLogin(ctx, callback); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("callback with incorrect nonce: %v", err)
	}
}

func TestDashboardSessionRefreshReplayAndSignOut(t *testing.T) {
	t.Parallel()
	f := newWebAuthFixture(t)

	for _, signOut := range []bool{false, true} {
		callback, binding := f.start(t, "good-code")
		ctx, info := webCall(t, binding)
		if _, err := f.client.CompleteWebLogin(ctx, callback); err != nil {
			t.Fatalf("complete web login: %v", err)
		}
		refresh := responseCookie(t, info, auth.WebRefreshCookie)
		ctx, info = webCall(t, refresh)
		if _, err := f.client.RefreshWebSession(ctx, &authv1.RefreshWebSessionRequest{}); err != nil {
			t.Fatalf("refresh web session: %v", err)
		}
		rotated := responseCookie(t, info, auth.WebRefreshCookie)
		access := responseCookie(t, info, auth.WebSessionCookie)
		if rotated.Value == refresh.Value {
			t.Fatal("refresh credential did not rotate")
		}
		if _, err := f.machine.GetToken(t.Context(), access.Value); err != nil {
			t.Fatalf("validate rotated access credential: %v", err)
		}

		if signOut {
			ctx, info = webCall(t, rotated)
			if _, err := f.client.SignOut(ctx, &authv1.SignOutRequest{}); err != nil {
				t.Fatalf("sign out with refresh cookie only: %v", err)
			}
			for _, name := range []string{auth.WebSessionCookie, auth.WebRefreshCookie} {
				if value := responseCookie(t, info, name); value.MaxAge >= 0 {
					t.Fatalf("sign out did not clear %s", name)
				}
			}
		} else {
			ctx, _ = webCall(t, refresh)
			if _, err := f.client.RefreshWebSession(
				ctx,
				&authv1.RefreshWebSessionRequest{},
			); connect.CodeOf(
				err,
			) != connect.CodeUnauthenticated {
				t.Fatalf("refresh credential replay: %v", err)
			}
		}
		if _, err := f.machine.GetToken(t.Context(), access.Value); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
			t.Fatalf("access credential still valid after revocation: %v", err)
		}
	}
}

func signInReason(t *testing.T, err error) string {
	t.Helper()
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("sign-in error code = %v, want unauthenticated: %v", connect.CodeOf(err), err)
	}
	info, ok := rpc.ErrorDetail[*errdetails.ErrorInfo](err)
	if !ok || info.GetDomain() != webSignInErrorDomain {
		return ""
	}
	return info.GetReason()
}

func TestWebSignInFailuresCarryTheirReason(t *testing.T) {
	t.Parallel()
	f := newWebAuthFixture(t)

	callback, binding := f.start(t, "good-code")
	canceled := url.Values{"error": {"access_denied"}, webLoginState: {stateOf(t, callback)}}
	ctx, _ := webCall(t, binding)
	_, err := f.client.CompleteWebLogin(ctx, &authv1.CompleteWebLoginRequest{
		CallbackUrl: dashboardOrigin + "/auth/callback?" + canceled.Encode(),
	})
	if got := signInReason(t, err); got != reasonSignInCanceled {
		t.Fatalf("canceled reason = %q, want %q", got, reasonSignInCanceled)
	}

	ctx, _ = webCall(t, binding)
	_, err = f.client.CompleteWebLogin(ctx, callback)
	if got := signInReason(t, err); got != reasonSignInExpired {
		t.Fatalf("spent state reason = %q, want %q", got, reasonSignInExpired)
	}

	callback, binding = f.start(t, "bad-nonce")
	ctx, _ = webCall(t, binding)
	_, err = f.client.CompleteWebLogin(ctx, callback)
	if got := signInReason(t, err); got != "" {
		t.Fatalf("nonce mismatch reason = %q, want none", got)
	}

	callback, _ = f.start(t, "good-code")
	ctx, _ = webCall(t)
	_, err = f.client.CompleteWebLogin(ctx, callback)
	if got := signInReason(t, err); got != reasonSignInInvalid {
		t.Fatalf("callback without binding reason = %q, want %q", got, reasonSignInInvalid)
	}
}

func TestClosedSignupReportsItsReason(t *testing.T) {
	t.Parallel()
	f := newWebAuthFixture(t)
	policy, err := auth.ParseSignupPolicy("closed")
	if err != nil {
		t.Fatal(err)
	}
	f.server.web.Resolver = auth.NewResolver(f.server.db, policy, limits.Defaults(), time.Hour)

	callback, binding := f.start(t, "good-code")
	ctx, _ := webCall(t, binding)
	_, err = f.client.CompleteWebLogin(ctx, callback)
	if got := signInReason(t, err); got != reasonSignupClosed {
		t.Fatalf("closed sign-up reason = %q, want %q", got, reasonSignupClosed)
	}
}

func stateOf(t *testing.T, req *authv1.CompleteWebLoginRequest) string {
	t.Helper()
	callback, err := url.Parse(req.GetCallbackUrl())
	if err != nil {
		t.Fatal(err)
	}
	return callback.Query().Get(webLoginState)
}

func TestSuspendedWebLoginReportsReasonWithoutIssuingSession(t *testing.T) {
	t.Parallel()
	f := newWebAuthFixture(t)
	callback, binding := f.start(t, "good-code")
	ctx, _ := webCall(t, binding)
	signedIn, err := f.client.CompleteWebLogin(ctx, callback)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := uuid.Parse(signedIn.GetUser().GetId())
	if err != nil {
		t.Fatal(err)
	}
	if _, statusErr := f.queries.SetUserStatus(
		t.Context(),
		genDb.SetUserStatusParams{ID: userID, Status: genDb.UserStatusSuspended},
	); statusErr != nil {
		t.Fatal(statusErr)
	}
	for _, withInvitation := range []bool{false, true} {
		if withInvitation {
			org, createErr := f.queries.CreateOrg(
				t.Context(),
				genDb.CreateOrgParams{Name: "inviting-org", CreatedBy: userID},
			)
			if createErr != nil {
				t.Fatal(createErr)
			}
			if _, inviteErr := f.queries.CreateInvitation(t.Context(), genDb.CreateInvitationParams{
				OrgID:     org.ID,
				Email:     dashboardEmail,
				Role:      genDb.OrgRoleMember,
				InvitedBy: userID,
				ExpiresAt: time.Now().Add(time.Hour),
			}); inviteErr != nil {
				t.Fatal(inviteErr)
			}
		}
		callback, binding = f.start(t, "good-code")
		suspendedCtx, info := webCall(t, binding)
		_, err = f.client.CompleteWebLogin(suspendedCtx, callback)
		if got := signInReason(t, err); got != "ACCOUNT_SUSPENDED" {
			t.Fatalf("suspended sign-in reason = %q, want ACCOUNT_SUSPENDED", got)
		}
		for _, value := range info.ResponseHeader().Values("Set-Cookie") {
			cookie, parseErr := http.ParseSetCookie(value)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if cookie.Value != "" {
				t.Fatalf("suspended sign-in issued a cookie: %s", cookie.Name)
			}
		}
		t.Logf("suspended sign-in (pending invitation=%t): ACCOUNT_SUSPENDED, no session cookies", withInvitation)
	}
	if _, statusErr := f.queries.SetUserStatus(
		t.Context(),
		genDb.SetUserStatusParams{ID: userID, Status: genDb.UserStatusActive},
	); statusErr != nil {
		t.Fatal(statusErr)
	}
	callback, binding = f.start(t, "good-code")
	ctx, _ = webCall(t, binding)
	restored, err := f.client.CompleteWebLogin(ctx, callback)
	if err != nil {
		t.Fatalf("reinstated sign-in: %v", err)
	}
	t.Log("reinstated account: sign-in succeeds")
	if restored.GetUser().GetId() != signedIn.GetUser().GetId() {
		t.Fatal("reinstated sign-in returned a different account")
	}
}
