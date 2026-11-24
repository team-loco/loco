package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
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
	pool    *pgxpool.Pool
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
	machine := testMachine(queries)

	identity := auth.Identity{Issuer: testIssuer, Subject: "sub-1", Email: "dev@example.test", EmailVerified: true}
	userID := signUp(t, openResolver(t, pool), identity.Subject, identity.Email)

	f := &authFixture{
		pool:    pool,
		machine: machine,
		queries: queries,
		clock:   time.Now(),
	}
	f.server = NewAuthServer(pool, queries, machine, "https://app.loco.test/", WebAuth{})
	f.server.now = func() time.Time { return f.clock }
	rpcServer := connect.NewServer()
	authv1connect.RegisterAuthServiceHandler(rpcServer, f.server)
	transport := connectinprocess.New(rpcServer)
	f.client = authv1connect.NewAuthServiceClient(connect.NewClient(transport))
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: userID}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		Entity: entity, UserID: userID, Method: genDb.DeploymentMethodWeb,
		ActorType: genDb.DeploymentActorTypeUser, ActorID: userID,
	})
	f.webCtx = auth.WithLogin(ctx, auth.Login{Identity: identity})
	return f
}

func (f *authFixture) issueWebSession(t *testing.T) (access, refresh string) {
	t.Helper()
	principal, _ := reqctx.PrincipalFrom(f.webCtx)
	access, refresh, err := f.machine.IssueWebSession(t.Context(), principal.UserID, netip.Addr{}, "dashboard")
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
			device, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
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
	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
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

type racingStore struct {
	authStore
	key    string
	during func()
}

func (c *racingStore) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.authStore.Get(ctx, key)
	if key == c.key && c.during != nil {
		during := c.during
		c.during = nil
		during()
	}
	return value, err
}

func TestDeviceLoginDescribesTheRequestingDevice(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	f.server.web.TrustedProxyHops = 1
	const agent = "loco/v1.2.3 (darwin arm64; build-box)"
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("User-Agent", agent)
	info.RequestHeader().Set("X-Forwarded-For", "203.0.113.9")
	started, err := f.client.StartDeviceLogin(ctx, &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, getErr := f.server.GetDeviceLogin(t.Context(), &authv1.GetDeviceLoginRequest{
		UserCode: started.GetUserCode(),
	}); getErr == nil {
		t.Fatal("device login described to a caller without a web session")
	}
	got, err := f.server.GetDeviceLogin(f.webCtx, &authv1.GetDeviceLoginRequest{UserCode: started.GetUserCode()})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.GetUserAgent() != agent || got.GetIpAddress() != "203.0.113.9" ||
		got.GetRequestedAt() == nil || !got.GetExpiresAt().AsTime().After(got.GetRequestedAt().AsTime()) {
		t.Fatalf("device login = %+v", got)
	}
	if _, getErr := f.server.GetDeviceLogin(f.webCtx, &authv1.GetDeviceLoginRequest{
		UserCode: "ZZZZ-ZZZZ",
	}); codeOf(getErr) != connect.CodeNotFound {
		t.Fatalf("unknown code: %v", getErr)
	}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
		UserCode: started.GetUserCode(),
	}); approveErr != nil {
		t.Fatalf("approve after describing: %v", approveErr)
	}
}

func TestDeviceLoginKeepsTheStartOfALongUserAgent(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("User-Agent", "loco/"+strings.Repeat("é", 64<<10))
	started, err := f.client.StartDeviceLogin(ctx, &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	got, err := f.server.GetDeviceLogin(f.webCtx, &authv1.GetDeviceLoginRequest{UserCode: started.GetUserCode()})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	agent := got.GetUserAgent()
	if len(agent) > maxStoredUserAgent {
		t.Fatalf("stored user agent is %d bytes, want at most %d", len(agent), maxStoredUserAgent)
	}
	if !strings.HasPrefix(agent, "loco/éé") {
		t.Fatalf("stored user agent starts %q, want the start of the header", agent[:min(len(agent), 16)])
	}
	if !utf8.ValidString(agent) {
		t.Fatal("stored user agent is not valid UTF-8")
	}
}

func TestDeviceLoginsStayWithinTheStoreBound(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	const bound = 4
	f.server.state = pgAuthStore{queries: f.queries, maxEntries: bound}
	var oldest *authv1.StartDeviceLoginResponse
	for i := range 10 {
		started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		if oldest == nil {
			oldest = started
		}
	}
	var codes, devices int
	if err := f.pool.QueryRow(t.Context(), `SELECT
		count(*) FILTER (WHERE key LIKE 'cli:user-code:%'), count(*) FILTER (WHERE key LIKE 'cli:device:%')
		FROM auth_states`).Scan(&codes, &devices); err != nil {
		t.Fatal(err)
	}
	if codes != bound || devices != bound {
		t.Fatalf("%d user codes and %d device rows after 10 device starts, want %d of each", codes, devices, bound)
	}
	poll := &authv1.PollDeviceLoginRequest{DeviceCode: oldest.GetDeviceCode()}
	if _, err := f.client.PollDeviceLogin(t.Context(), poll); codeOf(err) != connect.CodeNotFound {
		t.Fatalf("poll of an evicted device login: %v", err)
	}
}

func TestDevicePollFailsClosedWhenItCannotRecordThePoll(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	digest := bearer.Hash(started.GetDeviceCode())
	f.server.state = &failingSetStore{authStore: f.server.state, prefix: devicePollKey(digest), fails: 1}
	poll := &authv1.PollDeviceLoginRequest{DeviceCode: started.GetDeviceCode()}
	if _, pollErr := f.client.PollDeviceLogin(t.Context(), poll); codeOf(pollErr) != connect.CodeUnavailable {
		t.Fatalf("poll while the store fails: %v", pollErr)
	}
	if _, pollErr := f.client.PollDeviceLogin(t.Context(), poll); pollErr != nil {
		t.Fatalf("poll once the store recovers: %v", pollErr)
	}
}

func TestCLIDevicePollCannotOverwriteApproval(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	msg := started
	deviceDigest := bearer.Hash(msg.GetDeviceCode())
	racing := &racingStore{authStore: f.server.state, key: deviceKey(deviceDigest)}
	f.server.state = racing
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

// failingSetStore fails the next fails Set calls whose key starts with prefix.
type failingSetStore struct {
	authStore
	prefix string
	fails  int
}

func (c *failingSetStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if strings.HasPrefix(key, c.prefix) && c.fails > 0 {
		c.fails--
		return errors.New("store unavailable")
	}
	return c.authStore.Set(ctx, key, value, ttl)
}

func TestCLIDeviceApprovalRetriesAfterUnavailable(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	store := f.server.state
	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	f.server.state = &failingSetStore{
		authStore: store, prefix: deviceKey(bearer.Hash(started.GetDeviceCode())), fails: 1,
	}
	approve := &authv1.ApproveDeviceLoginRequest{UserCode: started.GetUserCode()}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, approve); codeOf(approveErr) != connect.CodeUnavailable {
		t.Fatalf("approve while the store fails: %v", approveErr)
	}
	var restored bool
	if scanErr := f.pool.QueryRow(t.Context(), "SELECT started_at IS NOT NULL FROM auth_states WHERE key = $1",
		userCodeKey(normalizeUserCode(started.GetUserCode()))).Scan(&restored); scanErr != nil || !restored {
		t.Fatalf("user code after a failed approval: restored as a started sign-in = %v, %v", restored, scanErr)
	}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, approve); approveErr != nil {
		t.Fatalf("retried approve: %v", approveErr)
	}
	f.clock = f.clock.Add(10 * time.Second)
	poll := &authv1.PollDeviceLoginRequest{DeviceCode: started.GetDeviceCode()}
	done, err := f.client.PollDeviceLogin(t.Context(), poll)
	if err != nil {
		t.Fatalf("poll after approval: %v", err)
	}
	f.assertWorks(t, done.GetTokens())
}

// failingGetStore fails the next Get of key.
type failingGetStore struct {
	authStore
	key    string
	failed bool
}

func (c *failingGetStore) Get(ctx context.Context, key string) ([]byte, error) {
	if key == c.key && !c.failed {
		c.failed = true
		return nil, errors.New("store unavailable")
	}
	return c.authStore.Get(ctx, key)
}

func TestCLIDeviceApprovalRetriesAfterAFailedRead(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	userCode := userCodeKey(normalizeUserCode(started.GetUserCode()))
	expiry := func() time.Time {
		var at time.Time
		if scanErr := f.pool.QueryRow(t.Context(),
			"SELECT expires_at FROM auth_states WHERE key = $1", userCode).Scan(&at); scanErr != nil {
			t.Fatal(scanErr)
		}
		return at
	}
	before := expiry()
	f.server.state = &failingGetStore{authStore: f.server.state, key: deviceKey(bearer.Hash(started.GetDeviceCode()))}
	approve := &authv1.ApproveDeviceLoginRequest{UserCode: started.GetUserCode()}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, approve); codeOf(approveErr) != connect.CodeUnavailable {
		t.Fatalf("approve while reading the device fails: %v", approveErr)
	}
	if after := expiry(); after.Sub(before).Abs() > time.Second {
		t.Fatalf("restored user code expires at %v, want its original expiry %v", after, before)
	}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, approve); approveErr != nil {
		t.Fatalf("retried approve: %v", approveErr)
	}
}

func TestCLIDeviceApprovalKeepsExpiry(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
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

type commitCheckingStore struct {
	authStore
	t        *testing.T
	pool     *pgxpool.Pool
	key      string
	approved int
}

func (c *commitCheckingStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if strings.HasPrefix(key, c.key) {
		query := "SELECT count(*) FROM events WHERE type = $1"
		err := c.pool.QueryRow(ctx, query, events.CLILoginApproved).Scan(&c.approved)
		if err != nil {
			c.t.Fatalf("count approval events: %v", err)
		}
	}
	return c.authStore.Set(ctx, key, value, ttl)
}

func TestCLILoginStoresCodeAfterApprovalCommits(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	loopback := &commitCheckingStore{authStore: f.server.state, t: t, pool: f.server.db, key: "cli:code:"}
	f.server.state = loopback
	_, err := f.server.ApproveCLILogin(f.webCtx, &authv1.ApproveCLILoginRequest{
		State: testState, CodeChallenge: challengeFor("a-verifier-that-is-long-enough-to-satisfy-rfc-7636-limits"),
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if loopback.approved != 1 {
		t.Fatalf("approval events committed when the loopback code was stored = %d, want 1", loopback.approved)
	}

	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	device := &commitCheckingStore{authStore: f.server.state, t: t, pool: f.server.db, key: "cli:device:"}
	f.server.state = device
	_, err = f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{UserCode: started.GetUserCode()})
	if err != nil {
		t.Fatalf("approve device: %v", err)
	}
	if device.approved != 2 {
		t.Fatalf("approval events committed when the device grant was stored = %d, want 2", device.approved)
	}
}

func TestSessionAddressIgnoresAClientSuppliedForwardedEntry(t *testing.T) {
	t.Parallel()
	f := newAuthFixture(t)
	f.server.web.TrustedProxyHops = 1
	started, err := f.client.StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, approveErr := f.server.ApproveDeviceLogin(f.webCtx, &authv1.ApproveDeviceLoginRequest{
		UserCode: started.GetUserCode(),
	}); approveErr != nil {
		t.Fatalf("approve: %v", approveErr)
	}
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("X-Forwarded-For", "198.51.100.66, 203.0.113.9")
	if _, pollErr := f.client.PollDeviceLogin(ctx, &authv1.PollDeviceLoginRequest{
		DeviceCode: started.GetDeviceCode(),
	}); pollErr != nil {
		t.Fatalf("poll: %v", pollErr)
	}
	principal, _ := reqctx.PrincipalFrom(f.webCtx)
	var address netip.Addr
	if scanErr := f.pool.QueryRow(
		t.Context(), "SELECT ip_address FROM session_tokens WHERE user_id = $1", principal.UserID,
	).Scan(&address); scanErr != nil || address.String() != "203.0.113.9" {
		t.Fatalf("session address = %v (%v), want the address the gateway appended", address, scanErr)
	}
}
