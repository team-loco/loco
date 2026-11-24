package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
)

// replica starts a second API instance on f's database, built the way the API process builds one.
func (f *webAuthFixture) replica(t *testing.T) (*AuthServer, authv1connect.AuthServiceClient) {
	t.Helper()
	server := NewAuthServer(f.pool, f.queries, f.machine, "https://app.loco.test/", f.server.web)
	server.now = func() time.Time { return f.clock }
	rpcServer := connect.NewServer()
	authv1connect.RegisterAuthServiceHandler(rpcServer, server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	listener := httptest.NewTLSServer(mux)
	t.Cleanup(listener.Close)
	return server, authv1connect.NewAuthServiceClient(
		connect.NewClient(connecthttp.NewTransport(listener.Client(), listener.URL)),
	)
}

func TestSignInFinishesOnAnotherReplica(t *testing.T) {
	t.Parallel()
	f := newWebAuthFixture(t)
	other, otherClient := f.replica(t)

	t.Run("web", func(t *testing.T) {
		callback, binding := f.start(t, "good-code")
		ctx, _ := webCall(t, binding)
		signedIn, err := otherClient.CompleteWebLogin(ctx, callback)
		if err != nil {
			t.Fatalf("complete web login on the other replica: %v", err)
		}
		if signedIn.GetUser().GetEmail() != dashboardEmail {
			t.Fatalf("signed in as %q", signedIn.GetUser().GetEmail())
		}
		ctx, _ = webCall(t, binding)
		_, replayErr := f.client.CompleteWebLogin(ctx, callback)
		if connect.CodeOf(replayErr) != connect.CodeUnauthenticated {
			t.Fatalf("callback replay on the first replica: %v", replayErr)
		}
	})

	t.Run("loopback", func(t *testing.T) {
		verifier := "a-replica-verifier-long-enough-to-satisfy-rfc-7636-limits"
		approved, err := f.server.ApproveCLILogin(f.webCtx, &authv1.ApproveCLILoginRequest{
			State: testState, CodeChallenge: challengeFor(verifier),
		})
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		exchange := &authv1.ExchangeCLICodeRequest{Code: approved.GetCode(), CodeVerifier: verifier}
		exchanged, err := otherClient.ExchangeCLICode(t.Context(), exchange)
		if err != nil {
			t.Fatalf("exchange on the other replica: %v", err)
		}
		f.assertWorks(t, exchanged.GetTokens())
		_, reuseErr := f.client.ExchangeCLICode(t.Context(), exchange)
		if codeOf(reuseErr) != connect.CodeUnauthenticated {
			t.Fatalf("reused code on the first replica: %v", reuseErr)
		}
	})

	t.Run("device", func(t *testing.T) {
		started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		poll := &authv1.PollDeviceLoginRequest{DeviceCode: started.GetDeviceCode()}
		pending, err := otherClient.PollDeviceLogin(t.Context(), poll)
		if err != nil || pending.GetTokens() != nil {
			t.Fatalf("poll on the other replica: %+v %v", pending, err)
		}
		if _, pollErr := f.client.PollDeviceLogin(t.Context(), poll); codeOf(pollErr) != connect.CodeResourceExhausted {
			t.Fatalf("fast poll on the first replica: %v", pollErr)
		}
		code := &authv1.GetDeviceLoginRequest{UserCode: started.GetUserCode()}
		if _, getErr := other.GetDeviceLogin(f.webCtx, code); getErr != nil {
			t.Fatalf("describe on the other replica: %v", getErr)
		}
		if _, approveErr := other.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
			UserCode: started.GetUserCode(),
		}); approveErr != nil {
			t.Fatalf("approve on the other replica: %v", approveErr)
		}
		if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
			UserCode: started.GetUserCode(),
		}); codeOf(approveErr) != connect.CodeNotFound {
			t.Fatalf("second approval on the first replica: %v", approveErr)
		}
		f.clock = f.clock.Add(10 * time.Second)
		done, err := f.client.PollDeviceLogin(t.Context(), poll)
		if err != nil {
			t.Fatalf("poll after approval: %v", err)
		}
		f.assertWorks(t, done.GetTokens())
		if _, pollErr := otherClient.PollDeviceLogin(t.Context(), poll); codeOf(pollErr) != connect.CodeNotFound {
			t.Fatalf("poll on the other replica after tokens issued: %v", pollErr)
		}
	})
}
