package loco

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
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
	req *connect.Request[authv1.ExchangeCLICodeRequest],
) (*connect.Response[authv1.ExchangeCLICodeResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256([]byte(req.Msg.GetCodeVerifier()))
	if req.Msg.GetCode() != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("bad code"))
	}
	return connect.NewResponse(&authv1.ExchangeCLICodeResponse{Tokens: &authv1.CLITokens{
		AccessToken: "loco_s_x", RefreshToken: "loco_r_x", ExpiresIn: 3600,
	}}), nil
}

func (f *fakeCLIAuth) PollDeviceLogin(
	_ context.Context,
	_ *connect.Request[authv1.PollDeviceLoginRequest],
) (*connect.Response[authv1.PollDeviceLoginResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	switch f.polls {
	case 1:
		return connect.NewResponse(&authv1.PollDeviceLoginResponse{}), nil
	case 2:
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("slow down"))
	default:
		return connect.NewResponse(
			&authv1.PollDeviceLoginResponse{Tokens: &authv1.CLITokens{AccessToken: "loco_s_d"}},
		), nil
	}
}

func newFakeCLIAuth(t *testing.T) (*fakeCLIAuth, authv1connect.AuthServiceClient) {
	t.Helper()
	fake := &fakeCLIAuth{}
	mux := http.NewServeMux()
	mux.Handle(authv1connect.NewAuthServiceHandler(fake))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return fake, authv1connect.NewAuthServiceClient(srv.Client(), srv.URL)
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
		go func() {
			req, reqErr := http.NewRequestWithContext(
				context.WithoutCancel(t.Context()), http.MethodGet,
				"http://127.0.0.1:"+q.Get("port")+"/callback?"+callback.Encode(), http.NoBody,
			)
			if reqErr != nil {
				t.Errorf("callback request: %v", reqErr)
				return
			}
			resp, getErr := http.DefaultClient.Do(req)
			if getErr != nil {
				t.Errorf("callback: %v", getErr)
				return
			}
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Errorf("close: %v", closeErr)
			}
		}()
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

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	_, err := browserLogin(ctx, client, "https://app.loco.test", fakeBrowser(t, fake, func(q url.Values) {
		q.Set("state", "forged")
	}))
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
