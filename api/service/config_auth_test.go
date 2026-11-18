package service

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
)

func TestGetConfigReportsDashboardSignInAvailability(t *testing.T) {
	t.Parallel()
	const clientID = "dashboard-client"
	for _, configured := range []bool{false, true} {
		issuers := []auth.IssuerConfig{{Issuer: "https://idp.example.test", Audience: clientID}}
		if configured {
			issuers[0].Web = &auth.WebConfig{ClientID: clientID, ClientSecret: "test secret", Scopes: "openid"}
		}
		server := NewConfigServer("", "", "", servicedefaults.Defaults{}, issuers, nil, 0, 0, nil, nil)
		config, err := server.GetConfig(t.Context(), &configv1.GetConfigRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if config.GetWebAuthEnabled() != configured || (config.GetAuth() != nil) != configured {
			t.Fatalf("dashboard sign-in availability fields disagree with issuer configuration")
		}
		if configured && !proto.Equal(config.GetAuth(), &configv1.AuthConfig{}) {
			t.Fatal("GetConfig exposed provider configuration")
		}
	}
}
