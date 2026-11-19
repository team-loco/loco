package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

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
				t.Parallel()
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
					started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
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
	_, replayErr := f.client.RefreshCLIToken(t.Context(), request)
	if connect.CodeOf(replayErr) != connect.CodeUnauthenticated {
		t.Fatalf("reuse = %v, want Unauthenticated", replayErr)
	}
	_, err = f.machine.GetToken(t.Context(), rotated.GetTokens().GetAccessToken())
	if !errors.Is(err, tvm.ErrInvalidExpiredToken) {
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

func TestConcurrentRefreshesOfOneTokenKeepTheSession(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	f.machine.Cfg.RefreshReuseGrace = time.Minute
	principal, _ := reqctx.PrincipalFrom(f.webCtx)
	_, refresh, err := f.machine.IssueSession(t.Context(), principal.UserID, netip.Addr{}, "")
	if err != nil {
		t.Fatal(err)
	}
	request := &authv1.RefreshCLITokenRequest{RefreshToken: refresh}
	const racers = 2
	results := make(chan error, racers)
	tokens := make(chan *authv1.CLITokens, racers)
	var start sync.WaitGroup
	start.Add(1)
	for range racers {
		go func() {
			start.Wait()
			response, refreshErr := f.client.RefreshCLIToken(t.Context(), request)
			results <- refreshErr
			if refreshErr == nil {
				tokens <- response.GetTokens()
			}
		}()
	}
	start.Done()
	var refreshed, aborted int
	for range racers {
		switch refreshErr := <-results; {
		case refreshErr == nil:
			refreshed++
		case codeOf(refreshErr) == connect.CodeAborted:
			aborted++
		default:
			t.Fatalf("concurrent refresh: %v", refreshErr)
		}
	}
	if refreshed != 1 || aborted != 1 {
		t.Fatalf("refreshed %d, aborted %d, want one of each", refreshed, aborted)
	}
	issued := <-tokens
	next, err := f.client.RefreshCLIToken(t.Context(), &authv1.RefreshCLITokenRequest{
		RefreshToken: issued.GetRefreshToken(),
	})
	if err != nil {
		t.Fatalf("refresh with the rotated token after the race: %v", err)
	}
	f.assertWorks(t, next.GetTokens())
}

func TestRefreshTokenReplayedAfterTheGraceRevokesTheSession(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	f.machine.Cfg.RefreshReuseGrace = 50 * time.Millisecond
	principal, _ := reqctx.PrincipalFrom(f.webCtx)
	_, refresh, err := f.machine.IssueSession(t.Context(), principal.UserID, netip.Addr{}, "")
	if err != nil {
		t.Fatal(err)
	}
	request := &authv1.RefreshCLITokenRequest{RefreshToken: refresh}
	rotated, err := f.client.RefreshCLIToken(t.Context(), request)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, replayErr := f.client.RefreshCLIToken(
		t.Context(),
		request,
	); codeOf(
		replayErr,
	) != connect.CodeUnauthenticated {
		t.Fatalf("replay after the grace: %v", replayErr)
	}
	if _, revokedErr := f.client.RefreshCLIToken(t.Context(), &authv1.RefreshCLITokenRequest{
		RefreshToken: rotated.GetTokens().GetRefreshToken(),
	}); codeOf(revokedErr) != connect.CodeUnauthenticated {
		t.Fatalf("refresh after the replay: %v", revokedErr)
	}
}
