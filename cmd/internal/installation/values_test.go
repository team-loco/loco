package installation

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestChartValuesQuoteOperatorConfiguration(t *testing.T) {
	const canary = "quoted\"\nmalicious: injected"
	config := Config{ControlPlane: ControlPlane{
		APIURL: "https://api.example.com", WebURL: "https://console.example.com",
		Auth:          Auth{Issuer: "https://auth.example.com", Audience: canary, WebClientID: canary},
		SourceBucket:  Bucket{Bucket: canary, Endpoint: "https://s3.example.com", Region: canary},
		Registry:      Registry{Endpoint: "https://registry.example.com"},
		Email:         Email{From: canary, TransportSecretRef: SecretKeyRef{Name: "mail", Key: "transport"}},
		Observability: Observability{IngestURL: "https://ingest.example.com"},
	}}
	data, err := controlPlaneValues(config, releaseFixture(), false)
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		API struct {
			Env   map[string]string `json:"env"`
			Email struct {
				From string `json:"from"`
			} `json:"email"`
		} `json:"api"`
	}
	var full map[string]any
	if parseErr := yaml.UnmarshalStrict(data, &full); parseErr != nil {
		t.Fatal(parseErr)
	}
	if _, exists := full["malicious"]; exists {
		t.Fatal("configuration injected a chart key")
	}
	if decodeErr := yaml.Unmarshal(data, &values); decodeErr != nil {
		t.Fatal(decodeErr)
	}

	if values.API.Env["LOCO_SOURCE_BUCKET_REGION"] != canary || values.API.Email.From != canary {
		t.Fatal("quoted value did not survive rendering")
	}
	if !strings.Contains(values.API.Env["AUTH_ISSUERS"], `quoted\"\nmalicious`) {
		t.Fatal("OIDC configuration changed during rendering")
	}
	worker := Worker{
		Region:     canary,
		AppsDomain: "apps.example.com",
		Builds:     Builds{PrivateEgress: []PrivateEgress{{CIDR: "10.0.0.1/32", Port: 443}}},
	}
	rendered, err := workerValues(config, worker, releaseFixture())
	if err != nil {
		t.Fatal(err)
	}
	var workerChart struct {
		Builds struct {
			PrivateEgress []PrivateEgress `json:"privateEgress"`
		} `json:"builds"`
		Global struct {
			Region string `json:"region"`
		} `json:"global"`
	}
	if err := yaml.Unmarshal(rendered, &workerChart); err != nil {
		t.Fatal(err)
	}
	if len(workerChart.Builds.PrivateEgress) != 1 || workerChart.Builds.PrivateEgress[0].CIDR != "10.0.0.1/32" ||
		workerChart.Builds.PrivateEgress[0].Port != 443 {
		t.Fatal("declared infrastructure egress was not preserved")
	}
	if workerChart.Global.Region != canary {
		t.Fatal("worker region changed during rendering")
	}
}
