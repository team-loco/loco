package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
)

func TestGetConfigReportsDashboardSignInAvailability(t *testing.T) {
	t.Parallel()
	const clientID = "dashboard-client"
	for _, configured := range []bool{false, true} {
		issuers := []auth.IssuerConfig{{Issuer: "https://idp.example.test", Audience: clientID}}
		if configured {
			issuers[0].Web = &auth.WebConfig{ClientID: clientID, ClientSecret: "test secret", Scopes: "openid"}
		}
		server := NewConfigServer("", "", "", servicedefaults.Defaults{}, issuers, nil, 0, 0, nil, nil, "", nil)
		config, err := server.GetConfig(t.Context(), &configv1.GetConfigRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if config.GetWebAuthEnabled() != configured {
			t.Fatalf("dashboard sign-in enabled = %t, want %t", config.GetWebAuthEnabled(), configured)
		}
	}
}

func TestGetConfigAnswersHTTPGetWithCacheControl(t *testing.T) {
	t.Parallel()
	server := connect.NewServer()
	configv1connect.RegisterConfigServiceHandler(
		server, NewConfigServer("", "", "", servicedefaults.Defaults{}, nil, nil, 0, 0, nil, nil, "", nil))
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	url := srv.URL + configv1connect.ConfigServiceGetConfigProcedure + "?encoding=json&connect=v1&message=%7B%7D"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
