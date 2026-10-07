package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
)

const (
	testIssuer = "https://auth.example.test"
	testState  = "state-0123456789"
)

type stubAdmin struct {
	state auth.IdentityState
	err   error
}

func (a *stubAdmin) State(context.Context, string) (auth.IdentityState, error) {
	return a.state, a.err
}

func (*stubAdmin) Delete(context.Context, string) error {
	return nil
}

type authFixture struct {
	server  *AuthServer
	machine *tvm.VendingMachine
	queries *genDb.Queries
	admin   *stubAdmin
	webCtx  context.Context
	clock   time.Time
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	store, err := cache.NewMemory(time.Hour)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Logf("close cache: %v", closeErr)
		}
	})

	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	identity := auth.Identity{Issuer: testIssuer, Subject: "sub-1", Email: "dev@example.test", EmailVerified: true}
	user, err := auth.NewResolver(pool, policy).Resolve(t.Context(), identity)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	f := &authFixture{
		machine: machine,
		queries: queries,
		admin:   &stubAdmin{state: auth.IdentityActive},
		clock:   time.Now(),
	}
	f.server = NewAuthServer(queries, machine, store, auth.Admins{testIssuer: f.admin}, "https://app.loco.test/")
	f.server.now = func() time.Time { return f.clock }
	ctx := context.WithValue(t.Context(), contextkeys.EntityKey, genDb.Entity{Type: genDb.EntityTypeUser, ID: user.ID})
	f.webCtx = context.WithValue(ctx, contextkeys.IdentityKey, identity)
	return f
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func codeOf(err error) connect.Code {
	return connect.CodeOf(err)
}

func (f *authFixture) assertWorks(t *testing.T, tokens *authv1.CLITokens) {
	t.Helper()
	if tokens.GetAccessToken() == "" || tokens.GetRefreshToken() == "" || tokens.GetExpiresIn() != 3600 {
		t.Fatalf("tokens = %+v", tokens)
	}
	entity, _, err := f.machine.GetToken(t.Context(), tokens.GetAccessToken())
	if err != nil {
		t.Fatalf("issued access token rejected: %v", err)
	}
	want, ok := f.webCtx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok || entity.ID != want.ID {
		t.Fatalf("token belongs to %s, want %s", entity.ID, want.ID)
	}
}

func TestCLILoopbackLogin(t *testing.T) {
	f := newAuthFixture(t)
	verifier := "a-verifier-that-is-long-enough-to-satisfy-rfc-7636-limits"

	approved, err := f.server.ApproveCLILogin(f.webCtx, connect.NewRequest(&authv1.ApproveCLILoginRequest{
		State:         testState,
		CodeChallenge: challengeFor(verifier),
	}))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	code := approved.Msg.GetCode()

	_, err = f.server.ExchangeCLICode(t.Context(), connect.NewRequest(&authv1.ExchangeCLICodeRequest{
		Code: code, CodeVerifier: "a-different-verifier-that-is-also-long-enough-for-pkce",
	}))
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong verifier: %v", err)
	}

	approved, err = f.server.ApproveCLILogin(f.webCtx, connect.NewRequest(&authv1.ApproveCLILoginRequest{
		State:         testState,
		CodeChallenge: challengeFor(verifier),
	}))
	if err != nil {
		t.Fatalf("approve again: %v", err)
	}
	code = approved.Msg.GetCode()
	exchanged, err := f.server.ExchangeCLICode(t.Context(), connect.NewRequest(&authv1.ExchangeCLICodeRequest{
		Code: code, CodeVerifier: verifier,
	}))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	f.assertWorks(t, exchanged.Msg.GetTokens())

	_, err = f.server.ExchangeCLICode(t.Context(), connect.NewRequest(&authv1.ExchangeCLICodeRequest{
		Code: code, CodeVerifier: verifier,
	}))
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("reused code: %v", err)
	}
}

func TestCLIApprovalNeedsWebSession(t *testing.T) {
	f := newAuthFixture(t)
	entity, ok := f.webCtx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		t.Fatal("no entity")
	}
	cliCtx := context.WithValue(t.Context(), contextkeys.EntityKey, entity)

	_, err := f.server.ApproveCLILogin(cliCtx, connect.NewRequest(&authv1.ApproveCLILoginRequest{
		State: testState, CodeChallenge: challengeFor("v"),
	}))
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("approve from a cli token: %v", err)
	}
	_, err = f.server.ApproveDeviceLogin(
		cliCtx,
		connect.NewRequest(&authv1.ApproveDeviceLoginRequest{UserCode: "BCDF-GHJK"}),
	)
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("device approve from a cli token: %v", err)
	}
	_, err = f.server.ApproveCLILogin(t.Context(), connect.NewRequest(&authv1.ApproveCLILoginRequest{}))
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous approve: %v", err)
	}
}

func TestCLIDeviceLogin(t *testing.T) {
	f := newAuthFixture(t)
	started, err := f.server.StartDeviceLogin(t.Context(), connect.NewRequest(&authv1.StartDeviceLoginRequest{}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	msg := started.Msg
	if msg.GetVerificationUri() != "https://app.loco.test/cli/device" || len(msg.GetUserCode()) != 9 ||
		msg.GetVerificationUriComplete() != "https://app.loco.test/cli/device?code="+msg.GetUserCode() {
		t.Fatalf("start = %+v", msg)
	}
	poll := connect.NewRequest(&authv1.PollDeviceLoginRequest{DeviceCode: msg.GetDeviceCode()})

	pending, err := f.server.PollDeviceLogin(t.Context(), poll)
	if err != nil || pending.Msg.GetTokens() != nil {
		t.Fatalf("first poll: %+v %v", pending, err)
	}
	if _, pollErr := f.server.PollDeviceLogin(t.Context(), poll); codeOf(pollErr) != connect.CodeResourceExhausted {
		t.Fatalf("fast poll: %v", pollErr)
	}

	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, connect.NewRequest(&authv1.ApproveDeviceLoginRequest{
		UserCode: "ZZZZ-ZZZZ",
	})); codeOf(approveErr) != connect.CodeNotFound {
		t.Fatalf("unknown code: %v", approveErr)
	}
	lowered := " " + toLower(msg.GetUserCode()) + " "
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, connect.NewRequest(&authv1.ApproveDeviceLoginRequest{
		UserCode: lowered,
	})); approveErr != nil {
		t.Fatalf("approve: %v", approveErr)
	}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, connect.NewRequest(&authv1.ApproveDeviceLoginRequest{
		UserCode: msg.GetUserCode(),
	})); codeOf(approveErr) != connect.CodeNotFound {
		t.Fatalf("second approval: %v", approveErr)
	}

	f.clock = f.clock.Add(10 * time.Second)
	done, err := f.server.PollDeviceLogin(t.Context(), poll)
	if err != nil {
		t.Fatalf("poll after approval: %v", err)
	}
	f.assertWorks(t, done.Msg.GetTokens())

	if _, pollErr := f.server.PollDeviceLogin(t.Context(), poll); codeOf(pollErr) != connect.CodeNotFound {
		t.Fatalf("poll after tokens issued: %v", pollErr)
	}
}

func toLower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 'a' - 'A'
		}
	}
	return string(out)
}

func (f *authFixture) login(t *testing.T) *authv1.CLITokens {
	t.Helper()
	verifier := "a-verifier-that-is-long-enough-to-satisfy-rfc-7636-limits"
	approved, err := f.server.ApproveCLILogin(f.webCtx, connect.NewRequest(&authv1.ApproveCLILoginRequest{
		State: testState, CodeChallenge: challengeFor(verifier),
	}))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	exchanged, err := f.server.ExchangeCLICode(t.Context(), connect.NewRequest(&authv1.ExchangeCLICodeRequest{
		Code: approved.Msg.GetCode(), CodeVerifier: verifier,
	}))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	return exchanged.Msg.GetTokens()
}

func TestCLIRefreshChecksIdentity(t *testing.T) {
	f := newAuthFixture(t)

	tokens := f.login(t)
	refreshed, err := f.server.RefreshCLIToken(t.Context(), connect.NewRequest(&authv1.RefreshCLITokenRequest{
		RefreshToken: tokens.GetRefreshToken(),
	}))
	if err != nil {
		t.Fatalf("refresh while active: %v", err)
	}
	f.assertWorks(t, refreshed.Msg.GetTokens())

	f.admin.err = errors.New("provider down")
	refreshed, err = f.server.RefreshCLIToken(t.Context(), connect.NewRequest(&authv1.RefreshCLITokenRequest{
		RefreshToken: refreshed.Msg.GetTokens().GetRefreshToken(),
	}))
	if err != nil {
		t.Fatalf("refresh while provider is down: %v", err)
	}

	f.admin.err = nil
	f.admin.state = auth.IdentityDisabled
	current := refreshed.Msg.GetTokens()
	_, err = f.server.RefreshCLIToken(t.Context(), connect.NewRequest(&authv1.RefreshCLITokenRequest{
		RefreshToken: current.GetRefreshToken(),
	}))
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("refresh of a disabled identity: %v", err)
	}
	if _, _, tokenErr := f.machine.GetToken(t.Context(), current.GetAccessToken()); tokenErr == nil {
		t.Fatal("access token of a disabled identity still works")
	}

	f.admin.state = auth.IdentityMissing
	other := f.login(t)
	_, err = f.server.RefreshCLIToken(t.Context(), connect.NewRequest(&authv1.RefreshCLITokenRequest{
		RefreshToken: other.GetRefreshToken(),
	}))
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("refresh of a deleted identity: %v", err)
	}
}
