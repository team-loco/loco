package main

import (
	"encoding/json"
	"testing"

	railway "github.com/railwayapp/railway-go-sdk"
)

func TestDocumentationSharesTheUIService(t *testing.T) {
	const expectedPort = 8080
	const commit = "0123456789012345678901234567890123456789"
	t.Setenv("DEPLOY_COMMIT", commit)
	tests := []struct {
		name       string
		uiDomain   string
		docsDomain string
		suffix     string
	}{
		{name: "production", uiDomain: "loco.build", docsDomain: "docs.loco.build"},
		{name: "staging", uiDomain: "staging.loco.build", docsDomain: "docs.staging.loco.build", suffix: "-staging"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := Railway(railway.Context{Environment: test.name})
			var dashboard *railway.Service
			for _, resource := range project.Resources {
				service, ok := resource.(railway.Service)
				if !ok {
					continue
				}
				if service.Name == "loco::cp-docs" {
					t.Fatal("documentation has a separate service")
				}
				if service.Name == "loco::cp-ui" {
					dashboard = &service
				}
			}
			if dashboard == nil {
				t.Fatal("UI service is absent")
			}
			source, ok := dashboard.Config["source"].(map[string]any)
			if !ok {
				t.Fatal("UI source is absent")
			}
			wantImage := registry + "/loco-ui:sha-" + commit + test.suffix
			if source["image"] != wantImage {
				t.Fatalf("image = %v, want %s", source["image"], wantImage)
			}
			network, ok := dashboard.Graph()["networking"].(map[string]any)
			if !ok {
				t.Fatal("UI networking is absent")
			}
			domains, ok := network["customDomains"].(map[string]any)
			if !ok {
				t.Fatal("UI domains are absent")
			}
			if len(domains) != 2 {
				t.Fatalf("custom domains = %v, want UI and docs", domains)
			}
			for _, hostname := range []string{test.uiDomain, test.docsDomain} {
				domain, ok := domains[hostname].(map[string]any)
				if !ok || domain["port"] != expectedPort {
					t.Fatalf("custom domain %s = %v, want port %d", hostname, domain, expectedPort)
				}
			}
		})
	}
}

func TestAPIIssuerTakesEmailVerificationFromTheProvider(t *testing.T) {
	for _, env := range []environment{{domainPrefix: ""}, {domainPrefix: "staging."}} {
		var issuers []struct {
			Audience          string         `json:"audience"`
			EmailVerification string         `json:"emailVerification"`
			Claims            map[string]any `json:"claims"`
		}
		if err := json.Unmarshal([]byte(env.authIssuers()), &issuers); err != nil {
			t.Fatalf("AUTH_ISSUERS: %v", err)
		}
		if len(issuers) != 1 {
			t.Fatalf("issuers = %+v", issuers)
		}
		issuer := issuers[0]
		if issuer.Audience == "" || issuer.EmailVerification != "admin" {
			t.Fatalf("issuer = %+v, want an audience and admin email verification", issuer)
		}
		if _, ok := issuer.Claims["emailVerified"]; ok {
			t.Fatalf("issuer maps emailVerified to a token claim: %v", issuer.Claims)
		}
	}
}
