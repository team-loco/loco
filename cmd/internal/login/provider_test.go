package login

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"net/http/httptest"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"connectrpc.com/connect/v2"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
)

type fakeCLIAuth struct {
	authv1connect.UnimplementedAuthServiceHandler

	mu        sync.Mutex
	challenge string
	polls     int
}

func (f *fakeCLIAuth) ExchangeCLICode(
	_ context.Context,
	req *authv1.ExchangeCLICodeRequest,
) (*authv1.ExchangeCLICodeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256([]byte(req.GetCodeVerifier()))
	if req.GetCode() != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
		return nil, connect.NewError(connect.CodeUnauthenticated, "bad code")
	}
	return &authv1.ExchangeCLICodeResponse{Tokens: &authv1.CLITokens{
		AccessToken: "loco_s_x", RefreshToken: "loco_r_x", ExpiresIn: 3600,
	}}, nil
}

func (f *fakeCLIAuth) PollDeviceLogin(
	_ context.Context,
	_ *authv1.PollDeviceLoginRequest,
) (*authv1.PollDeviceLoginResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	switch f.polls {
	case 1:
		return &authv1.PollDeviceLoginResponse{}, nil
	case 2:
		return nil, connect.NewError(connect.CodeResourceExhausted, "slow down")
	default:
		return &authv1.PollDeviceLoginResponse{Tokens: &authv1.CLITokens{AccessToken: "loco_s_d"}}, nil
	}
}

func newFakeCLIAuth(t *testing.T) (*fakeCLIAuth, authv1connect.AuthServiceClient) {
	t.Helper()
	fake := &fakeCLIAuth{}
	server := connect.NewServer()
	authv1connect.RegisterAuthServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	return fake, authv1connect.NewAuthServiceClient(connect.NewClient(transport))
}

func fakeBrowser(t *testing.T, fake *fakeCLIAuth, mutate func(url.Values)) func(context.Context, string) error {
	t.Helper()
	return func(_ context.Context, raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(raw, "https://app.loco.test/cli/login?") {
			return fmt.Errorf("unexpected login url %s", raw)
		}
		q := u.Query()
		fake.mu.Lock()
		fake.challenge = q.Get("challenge")
		fake.mu.Unlock()
		callback := url.Values{"code": {"the-code"}, "state": {q.Get("state")}}
		mutate(callback)
		// The loopback server is serving before the browser opens, so the callback completes here.
		req, err := http.NewRequestWithContext(
			t.Context(), http.MethodGet,
			"http://127.0.0.1:"+q.Get("port")+"/callback?"+callback.Encode(), http.NoBody,
		)
		if err != nil {
			t.Errorf("callback request: %v", err)
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("callback: %v", err)
			return err
		}
		if err = resp.Body.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		return nil
	}
}

func TestBrowserLoginExchangesCode(t *testing.T) {
	fake, client := newFakeCLIAuth(t)
	tokens, err := browserLogin(t.Context(), client, "https://app.loco.test", fakeBrowser(t, fake, func(url.Values) {}))
	if err != nil {
		t.Fatalf("browser login: %v", err)
	}
	if tokens.GetAccessToken() != "loco_s_x" || tokens.GetRefreshToken() != "loco_r_x" {
		t.Fatalf("tokens = %+v", tokens)
	}
}

func TestBrowserLoginRejectsWrongStateAndCancellation(t *testing.T) {
	fake, client := newFakeCLIAuth(t)

	expiring := expiringContext{Context: t.Context(), done: make(chan struct{})}
	forged := fakeBrowser(t, fake, func(q url.Values) {
		q.Set("state", "forged")
	})
	_, err := browserLogin(expiring, client, "https://app.loco.test", func(ctx context.Context, raw string) error {
		// The login times out only after the forged callback was answered.
		defer close(expiring.done)
		return forged(ctx, raw)
	})
	if !errors.Is(err, errLoginTimedOut) {
		t.Fatalf("forged state: %v", err)
	}

	_, err = browserLogin(t.Context(), client, "https://app.loco.test", fakeBrowser(t, fake, func(q url.Values) {
		q.Del("code")
		q.Set("error", "access_denied")
	}))
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("canceled login: %v", err)
	}
}

func TestPollDeviceLoginBacksOff(t *testing.T) {
	fake, client := newFakeCLIAuth(t)
	tokens, err := pollDeviceLogin(t.Context(), client, "device", time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if tokens.GetAccessToken() != "loco_s_d" || fake.polls != 3 {
		t.Fatalf("tokens = %+v after %d polls", tokens, fake.polls)
	}
}

// expiringContext reports a passed deadline once done closes.
type expiringContext struct {
	context.Context
	done chan struct{}
}

func (c expiringContext) Done() <-chan struct{} { return c.done }

func (c expiringContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func TestUserAgentNamesTheCLIVersionSystemAndHost(t *testing.T) {
	t.Parallel()
	got := userAgent(Options{Command: "loco login", Version: "v1.2.3"})
	if !regexp.MustCompile(`^loco/v1\.2\.3 \([a-z0-9]+ [a-z0-9]+; [^();]+\)$`).MatchString(got) {
		t.Fatalf("user agent = %q", got)
	}
}

func TestDeviceLoginCopiesUserCode(t *testing.T) {
	const code = "ABCD-EFGH"
	m := initialModel(code, "https://loco.test/device", func() tea.Msg { return authSuccessMsg{} })
	msgs, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatal("device login did not initialize a batch of commands")
	}
	for _, cmd := range msgs {
		if reflect.DeepEqual(cmd(), tea.SetClipboard(code)()) {
			return
		}
	}
	t.Fatal("device login did not request copying the user code")
}
