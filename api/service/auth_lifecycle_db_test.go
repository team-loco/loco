package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
)

func TestApprovedCLILoginCannotOutliveAccount(t *testing.T) {
	t.Parallel()
	for _, flow := range []string{"loopback", "device"} {
		for _, change := range []string{"account removed", "suspended", "identity removed"} {
			t.Run(flow+"/"+change, func(t *testing.T) {
				f := newAuthHTTPFixture(t)
				const verifier = "a-long-test-verifier-for-the-account-lifecycle-flow"
				var exchange func() error
				if flow == "loopback" {
					approval, err := f.server.ApproveCLILogin(f.webCtx, &authv1.ApproveCLILoginRequest{
						State: testState, CodeChallenge: challengeFor(verifier),
					})
					if err != nil {
						t.Fatal(err)
					}
					exchange = func() error {
						_, err := f.client.ExchangeCLICode(t.Context(), &authv1.ExchangeCLICodeRequest{
							Code: approval.GetCode(), CodeVerifier: verifier,
						})
						return err
					}
				} else {
					started, err := f.server.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
					if err != nil {
						t.Fatal(err)
					}
					if _, err = f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
						UserCode: started.GetUserCode(),
					}); err != nil {
						t.Fatal(err)
					}
					exchange = func() error {
						_, err := f.client.PollDeviceLogin(t.Context(), &authv1.PollDeviceLoginRequest{
							DeviceCode: started.GetDeviceCode(),
						})
						return err
					}
				}
				p, _ := reqctx.PrincipalFrom(f.webCtx)
				switch change {
				case "account removed":
					if err := f.queries.DeleteUser(t.Context(), p.Entity.ID); err != nil {
						t.Fatal(err)
					}
				case "suspended":
					if _, err := f.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
						ID: p.Entity.ID, Status: genDb.UserStatusSuspended,
					}); err != nil {
						t.Fatal(err)
					}
				case "identity removed":
					if _, err := f.server.db.Exec(
						t.Context(),
						"DELETE FROM identities WHERE user_id = $1",
						p.Entity.ID,
					); err != nil {
						t.Fatal(err)
					}
				}
				if err := exchange(); connect.CodeOf(err) != connect.CodeUnauthenticated {
					t.Fatalf("exchange after %s = %v, want Unauthenticated", change, err)
				}
				var sessions int
				if err := f.server.db.QueryRow(t.Context(), "SELECT count(*) FROM session_tokens WHERE user_id = $1", p.Entity.ID).
					Scan(&sessions); err != nil {
					t.Fatal(err)
				}
				if sessions != 0 {
					t.Fatal("invalidated approval minted a session")
				}
			})
		}
	}
}

func TestCLIRefreshRefusesSuspendedAccount(t *testing.T) {
	t.Parallel()
	f := newAuthHTTPFixture(t)
	tokens := f.login(t)
	p, _ := reqctx.PrincipalFrom(f.webCtx)
	if _, err := f.queries.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
		ID: p.Entity.ID, Status: genDb.UserStatusSuspended,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.RefreshCLIToken(t.Context(), &authv1.RefreshCLITokenRequest{
		RefreshToken: tokens.GetRefreshToken(),
	}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("refresh suspended account = %v, want Unauthenticated", err)
	}
}

func TestCLIRefreshRejectsWebSession(t *testing.T) {
	t.Parallel()
	f := newAuthHTTPFixture(t)
	webAccess, webRefresh := f.issueWebSession(t)
	_, err := f.client.RefreshCLIToken(t.Context(), &authv1.RefreshCLITokenRequest{RefreshToken: webRefresh})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("CLI refresh of web session = %v, want Unauthenticated", err)
	}
	if _, err = f.machine.GetToken(t.Context(), webAccess); err != nil {
		t.Fatalf("CLI refresh mutated the web session: %v", err)
	}
}

func TestRefreshCommitsRevocationOnReuse(t *testing.T) {
	t.Parallel()
	f := newAuthHTTPFixture(t)
	token := f.login(t).GetRefreshToken()
	request := &authv1.RefreshCLITokenRequest{RefreshToken: token}
	rotated, err := f.client.RefreshCLIToken(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.RefreshCLIToken(t.Context(), request); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("reuse = %v, want Unauthenticated", err)
	}
	if _, err := f.machine.GetToken(t.Context(), rotated.GetTokens().GetAccessToken()); !errors.Is(err, tvm.ErrInvalidExpiredToken) {
		t.Fatalf("rotated access token = %v, want revoked", err)
	}
}

func newAuthHTTPFixture(t *testing.T) *authFixture {
	t.Helper()
	f := newAuthFixture(t)
	server := connect.NewServer(interceptor.NewErrorInterceptor())
	authv1connect.RegisterAuthServiceHandler(server, f.server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	transport := connecthttp.NewTransport(httpServer.Client(), httpServer.URL)
	f.client = authv1connect.NewAuthServiceClient(connect.NewClient(transport))
	return f
}
