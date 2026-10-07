package main

import (
	"testing"

	railway "github.com/railwayapp/railway-go-sdk"
)

func TestDocumentationServiceForEachEnvironment(t *testing.T) {
	const commit = "0123456789012345678901234567890123456789"
	t.Setenv("DEPLOY_COMMIT", commit)
	tests := []struct {
		name   string
		domain string
		suffix string
	}{
		{name: "production", domain: "docs.loco.build"},
		{name: "staging", domain: "docs.staging.loco.build", suffix: "-staging"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := Railway(railway.Context{Environment: test.name})
			var documentation *railway.Service
			for _, resource := range project.Resources {
				service, ok := resource.(railway.Service)
				if ok && service.Name == "loco::cp-docs" {
					documentation = &service
					break
				}
			}
			if documentation == nil {
				t.Fatal("documentation service is absent")
			}
			source, ok := documentation.Config["source"].(map[string]any)
			if !ok {
				t.Fatal("documentation source is absent")
			}
			wantImage := registry + "/loco-docs:sha-" + commit + test.suffix
			if source["image"] != wantImage {
				t.Fatalf("image = %v, want %s", source["image"], wantImage)
			}
			network, ok := documentation.Config["networking"].(map[string]any)
			if !ok {
				t.Fatal("documentation networking is absent")
			}
			domains, ok := network["customDomains"].(map[string]any)
			if !ok {
				t.Fatal("documentation domains are absent")
			}
			if len(domains) != 1 {
				t.Fatalf("custom domains = %v, want exactly one", domains)
			}
			domain, ok := domains[test.domain].(map[string]any)
			if !ok || domain["port"] != docsPort {
				t.Fatalf("custom domain = %v, want port 8080", domain)
			}
			deploy, ok := documentation.Config["deploy"].(map[string]any)
			if !ok || deploy["healthcheckPath"] != "/health" {
				t.Fatalf("healthcheck = %v, want /health", deploy)
			}
		})
	}
}
