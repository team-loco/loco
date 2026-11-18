package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/internal/bearer"
)

const (
	testIssuer = "https://auth.example.test"
	testState  = "state-0123456789"
)

type authFixture struct {
	server  *AuthServer
	client  authv1connect.AuthServiceClient
	machine *tvm.VendingMachine
	queries *genDb.Queries
	webCtx  context.Context
	clock   time.Time
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: 24 * time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	store, err := cache.NewMemory()
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Logf("close cache: %v", closeErr)
		}
	})

	policy, err := auth.ParseSignupPolicy("open")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	identity := auth.Identity{Issuer: testIssuer, Subject: "sub-1", Email: "dev@example.test", EmailVerified: true}
	user, _, err := auth.NewResolver(pool, policy, limits.Defaults(), time.Hour).Resolve(t.Context(), identity)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	f := &authFixture{
		machine: machine,
		queries: queries,
		clock:   time.Now(),
	}
	f.server = NewAuthServer(pool, queries, machine, store, "https://app.loco.test/", WebAuth{})
	f.server.now = func() time.Time { return f.clock }
	rpcServer := connect.NewServer()
	authv1connect.RegisterAuthServiceHandler(rpcServer, f.server)
	transport := connectinprocess.New(rpcServer)
	f.client = authv1connect.NewAuthServiceClient(connect.NewClient(transport))
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: user.ID}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		Entity: entity, UserID: user.ID, Method: genDb.DeploymentMethodWeb,
		ActorType: genDb.DeploymentActorTypeUser, ActorID: user.ID,
	})
	f.webCtx = auth.WithLogin(ctx, auth.Login{Identity: identity})
	return f
}

func (f *authFixture) issueWebSession(t *testing.T) (access, refresh string) {
	t.Helper()
	principal, _ := reqctx.PrincipalFrom(f.webCtx)
	identities, err := f.queries.ListIdentitiesForUser(t.Context(), principal.UserID)
	if err != nil || len(identities) != 1 {
		t.Fatalf("fixture identities = %v, error = %v", identities, err)
	}
	access, refresh, err = f.machine.IssueWebSession(
		t.Context(), principal.UserID, identities[0].ID, netip.Addr{}, "dashboard",
	)
	if err != nil {
		t.Fatal(err)
	}
	return access, refresh
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func codeOf(err error) connect.Code {
	if public := publicError(err); public != nil {
		return public.Code()
	}
	return connect.CodeOf(err)
}

func (f *authFixture) assertWorks(t *testing.T, tokens *authv1.CLITokens) {
	t.Helper()
	if tokens.GetAccessToken() == "" || tokens.GetRefreshToken() == "" || tokens.GetExpiresIn() != 3600 {
		t.Fatalf("tokens = %+v", tokens)
	}
	principal, err := f.machine.GetToken(t.Context(), tokens.GetAccessToken())
	if err != nil {
		t.Fatalf("issued access token rejected: %v", err)
	}
	want, ok := reqctx.PrincipalFrom(f.webCtx)
	if !ok || principal.Entity.ID != want.Entity.ID {
		t.Fatalf("token belongs to %s, want %s", principal.Entity.ID, want.Entity.ID)
	}
}

func TestCLILoopbackLogin(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	verifier := "a-verifier-that-is-long-enough-to-satisfy-rfc-7636-limits"

	approved, err := f.server.ApproveCLILogin(f.webCtx, &authv1.ApproveCLILoginRequest{
		State:         testState,
		CodeChallenge: challengeFor(verifier),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	code := approved.GetCode()

	_, err = f.client.ExchangeCLICode(t.Context(), &authv1.ExchangeCLICodeRequest{
		Code: code, CodeVerifier: "a-different-verifier-that-is-also-long-enough-for-pkce",
	})
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("wrong verifier: %v", err)
	}

	approved, err = f.server.ApproveCLILogin(f.webCtx, &authv1.ApproveCLILoginRequest{
		State:         testState,
		CodeChallenge: challengeFor(verifier),
	})
	if err != nil {
		t.Fatalf("approve again: %v", err)
	}
	code = approved.GetCode()
	exchanged, err := f.client.ExchangeCLICode(t.Context(), &authv1.ExchangeCLICodeRequest{
		Code: code, CodeVerifier: verifier,
	})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	f.assertWorks(t, exchanged.GetTokens())

	_, err = f.client.ExchangeCLICode(t.Context(), &authv1.ExchangeCLICodeRequest{
		Code: code, CodeVerifier: verifier,
	})
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("reused code: %v", err)
	}
}

func TestCLIApprovalNeedsWebSession(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	web, _ := f.issueWebSession(t)
	cli := f.login(t).GetAccessToken()
	for _, tc := range []struct {
		name  string
		token string
		want  connect.Code
	}{
		{"dashboard session", web, 0},
		{"CLI session", cli, connect.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			principal, err := f.machine.GetToken(t.Context(), tc.token)
			if err != nil {
				t.Fatal(err)
			}
			ctx := reqctx.WithPrincipal(t.Context(), principal)
			_, err = f.server.ApproveCLILogin(ctx, &authv1.ApproveCLILoginRequest{
				State: testState, CodeChallenge: challengeFor("v"),
			})
			if got := codeOf(err); (tc.want == 0 && err != nil) || (tc.want != 0 && got != tc.want) {
				t.Fatalf("approve loopback = %v, want %v", err, tc.want)
			}
			device, err := f.server.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.server.ApproveDeviceLogin(ctx, &authv1.ApproveDeviceLoginRequest{
				UserCode: device.GetUserCode(),
			})
			if got := codeOf(err); (tc.want == 0 && err != nil) || (tc.want != 0 && got != tc.want) {
				t.Fatalf("approve device = %v, want %v", err, tc.want)
			}
		})
	}
	_, err := f.server.ApproveCLILogin(t.Context(), &authv1.ApproveCLILoginRequest{})
	if codeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous approve: %v", err)
	}
}

func TestCLIDeviceLogin(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	started, err := f.server.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	msg := started
	if msg.GetVerificationUri() != "https://app.loco.test/cli/device" || len(msg.GetUserCode()) != 9 ||
		msg.GetVerificationUriComplete() != "https://app.loco.test/cli/device?code="+msg.GetUserCode() {
		t.Fatalf("start = %+v", msg)
	}
	poll := &authv1.PollDeviceLoginRequest{DeviceCode: msg.GetDeviceCode()}

	pending, err := f.client.PollDeviceLogin(t.Context(), poll)
	if err != nil || pending.GetTokens() != nil {
		t.Fatalf("first poll: %+v %v", pending, err)
	}
	if _, pollErr := f.client.PollDeviceLogin(t.Context(), poll); codeOf(pollErr) != connect.CodeResourceExhausted {
		t.Fatalf("fast poll: %v", pollErr)
	}

	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
		UserCode: "ZZZZ-ZZZZ",
	}); codeOf(approveErr) != connect.CodeNotFound {
		t.Fatalf("unknown code: %v", approveErr)
	}
	lowered := " " + toLower(msg.GetUserCode()) + " "
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
		UserCode: lowered,
	}); approveErr != nil {
		t.Fatalf("approve: %v", approveErr)
	}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
		UserCode: msg.GetUserCode(),
	}); codeOf(approveErr) != connect.CodeNotFound {
		t.Fatalf("second approval: %v", approveErr)
	}

	f.clock = f.clock.Add(10 * time.Second)
	done, err := f.client.PollDeviceLogin(t.Context(), poll)
	if err != nil {
		t.Fatalf("poll after approval: %v", err)
	}
	f.assertWorks(t, done.GetTokens())

	if _, pollErr := f.client.PollDeviceLogin(t.Context(), poll); codeOf(pollErr) != connect.CodeNotFound {
		t.Fatalf("poll after tokens issued: %v", pollErr)
	}
}

type racingCache struct {
	cache.Cache
	key    string
	during func()
}

func (c *racingCache) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.Cache.Get(ctx, key)
	if key == c.key && c.during != nil {
		during := c.during
		c.during = nil
		during()
	}
	return value, err
}

func TestCLIDevicePollCannotOverwriteApproval(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	started, err := f.server.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	msg := started
	deviceDigest := bearer.Hash(msg.GetDeviceCode())
	racing := &racingCache{Cache: f.server.cache, key: deviceKey(deviceDigest)}
	f.server.cache = racing
	racing.during = func() {
		_, approveErr := f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
			UserCode: msg.GetUserCode(),
		})
		if approveErr != nil {
			t.Errorf("approve during poll: %v", approveErr)
		}
	}
	poll := &authv1.PollDeviceLoginRequest{DeviceCode: msg.GetDeviceCode()}

	pending, err := f.client.PollDeviceLogin(t.Context(), poll)
	if err != nil || pending.GetTokens() != nil {
		t.Fatalf("poll racing the approval: %+v %v", pending, err)
	}

	f.clock = f.clock.Add(10 * time.Second)
	done, err := f.client.PollDeviceLogin(t.Context(), poll)
	if err != nil {
		t.Fatalf("poll after approval: %v", err)
	}
	f.assertWorks(t, done.GetTokens())
}

func TestCLIDeviceApprovalKeepsExpiry(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	started, err := f.server.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	f.clock = f.clock.Add(deviceCodeTTL)
	_, err = f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
		UserCode: started.GetUserCode(),
	})
	if codeOf(err) != connect.CodeNotFound {
		t.Fatalf("approve after the device code expired: %v", err)
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
	approved, err := f.server.ApproveCLILogin(f.webCtx, &authv1.ApproveCLILoginRequest{
		State: testState, CodeChallenge: challengeFor(verifier),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	exchanged, err := f.client.ExchangeCLICode(t.Context(), &authv1.ExchangeCLICodeRequest{
		Code: approved.GetCode(), CodeVerifier: verifier,
	})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	return exchanged.GetTokens()
}

func TestCLIRefreshIssuesWorkingTokens(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)

	tokens := f.login(t)
	refreshed, err := f.server.RefreshCLIToken(t.Context(), &authv1.RefreshCLITokenRequest{
		RefreshToken: tokens.GetRefreshToken(),
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	f.assertWorks(t, refreshed.GetTokens())
}

type commitCheckingCache struct {
	cache.Cache
	t        *testing.T
	pool     *pgxpool.Pool
	key      string
	approved int
}

func (c *commitCheckingCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if strings.HasPrefix(key, c.key) {
		query := "SELECT count(*) FROM events WHERE type = $1"
		err := c.pool.QueryRow(ctx, query, events.CLILoginApproved).Scan(&c.approved)
		if err != nil {
			c.t.Fatalf("count approval events: %v", err)
		}
	}
	return c.Cache.Set(ctx, key, value, ttl)
}

func TestCLILoginStoresCodeAfterApprovalCommits(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	loopback := &commitCheckingCache{Cache: f.server.cache, t: t, pool: f.server.db, key: "cli:code:"}
	f.server.cache = loopback
	_, err := f.server.ApproveCLILogin(f.webCtx, &authv1.ApproveCLILoginRequest{
		State: testState, CodeChallenge: challengeFor("a-verifier-that-is-long-enough-to-satisfy-rfc-7636-limits"),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if loopback.approved != 1 {
		t.Fatalf("approval events committed when the loopback code was stored = %d, want 1", loopback.approved)
	}

	started, err := f.server.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	device := &commitCheckingCache{Cache: f.server.cache, t: t, pool: f.server.db, key: "cli:device:"}
	f.server.cache = device
	_, err = f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{UserCode: started.GetUserCode()})
	if err != nil {
		t.Fatalf("approve device: %v", err)
	}
	if device.approved != 2 {
		t.Fatalf("approval events committed when the device grant was stored = %d, want 2", device.approved)
	}
}
