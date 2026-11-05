package interceptor

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/api/auth"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
)

type recordSink struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*recordSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *recordSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
	return nil
}

func (s *recordSink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *recordSink) WithGroup(string) slog.Handler { return s }

func (s *recordSink) loud() []slog.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var loud []slog.Record
	for _, r := range s.records {
		if r.Level >= slog.LevelWarn {
			loud = append(loud, r)
		}
	}
	return loud
}

func TestFailedAuthenticationIsLoggedOnce(t *testing.T) {
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	broken := newProviderFixture(t, policy)
	valid := broken.issuer.Sign("k1", broken.issuer.Claims("sub-1", "dev@example.test", true))
	broken.pool.Close()
	healthy := newProviderFixture(t, policy)

	cases := []struct {
		name      string
		fixture   *providerFixture
		token     string
		code      connect.Code
		wantLevel slog.Level
		wantCount int
	}{
		{"server failure", broken, valid, connect.CodeInternal, slog.LevelError, 1},
		{"rejected token", healthy, "loco_s_doesnotexist", connect.CodeUnauthenticated, 0, 0},
		{"missing token", healthy, "", connect.CodeUnauthenticated, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := connect.NewServer(NewContextInterceptor().Interceptor(), tc.fixture.interceptor.Interceptor())
			configv1connect.RegisterConfigServiceHandler(server, &contextTestServer{})
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			client := configv1connect.NewConfigServiceClient(
				connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)),
			)

			sink := &recordSink{}
			previous := slog.Default()
			slog.SetDefault(slog.New(sink))
			t.Cleanup(func() { slog.SetDefault(previous) })

			ctx, info := connect.NewClientContext(t.Context())
			if tc.token != "" {
				info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
			}
			_, err := client.GetConfig(ctx, &configv1.GetConfigRequest{})
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("code = %s (%v), want %s", connect.CodeOf(err), err, tc.code)
			}
			loud := sink.loud()
			if len(loud) != tc.wantCount {
				for _, r := range loud {
					t.Logf("%s %s", r.Level, r.Message)
				}
				t.Fatalf("%d WARN or ERROR records, want %d", len(loud), tc.wantCount)
			}
			if tc.wantCount == 1 && loud[0].Level != tc.wantLevel {
				t.Fatalf("level = %s, want %s", loud[0].Level, tc.wantLevel)
			}
		})
	}
}
