package installation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestChartValuesQuoteOperatorConfiguration(t *testing.T) {
	const canary = "quoted\"\nmalicious: injected"
	config := Config{ControlPlane: ControlPlane{
		APIURL: "https://api.example.com", WebURL: "https://console.example.com",
		Auth:          Auth{Issuer: "https://auth.example.com", ClientID: canary},
		SourceBucket:  Bucket{Bucket: canary, Endpoint: "https://s3.example.com", Region: canary},
		Registry:      Registry{Endpoint: "https://registry.example.com"},
		Observability: Observability{IngestURL: "https://ingest.example.com"},
	}}
	data, err := controlPlaneValues(config, releaseFixture(), false)
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		API struct {
			Env map[string]string `json:"env"`
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

	if values.API.Env["LOCO_SOURCE_BUCKET_REGION"] != canary {
		t.Fatal("quoted value did not survive rendering")
	}
	if !strings.Contains(values.API.Env["AUTH_ISSUERS"], `quoted\"\nmalicious`) {
		t.Fatal("OIDC configuration changed during rendering")
	}
	var issuers []struct {
		Audience string `json:"audience"`
		Web      struct {
			ClientID string `json:"clientId"`
		} `json:"web"`
	}
	if decodeErr := json.Unmarshal([]byte(values.API.Env["AUTH_ISSUERS"]), &issuers); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if len(issuers) != 1 || issuers[0].Audience != canary || issuers[0].Web.ClientID != canary {
		t.Fatal("API and dashboard must use the configured OIDC client")
	}
	worker := Worker{
		MetricsServer: MetricsServer{Enabled: true, KubeletInsecureTLS: false},
		Region:        canary,
		Role:          "builds",
		AppsDomain:    "apps.example.com",
		Builds:        Builds{PrivateEgress: []PrivateEgress{{CIDR: "10.0.0.1/32", Port: 443}}},
	}
	rendered, err := workerValues(config, worker, releaseFixture())
	if err != nil {
		t.Fatal(err)
	}
	var workerChart struct {
		Cluster struct {
			Role string `json:"role"`
		} `json:"cluster"`
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
	if workerChart.Cluster.Role != "builds" {
		t.Fatalf("worker role rendered as %q", workerChart.Cluster.Role)
	}
}

func TestWorkerMetricsServerValues(t *testing.T) {
	for _, tt := range []struct {
		name string
		spec MetricsServer
		want string
	}{
		{name: "disabled secure", spec: MetricsServer{Enabled: false, KubeletInsecureTLS: false},
			want: "enabled: false\nargs: []"},
		{name: "enabled secure", spec: MetricsServer{Enabled: true, KubeletInsecureTLS: false},
			want: "enabled: true\nargs: []"},
		{name: "enabled insecure", spec: MetricsServer{Enabled: true, KubeletInsecureTLS: true},
			want: "enabled: true\nargs: [--kubelet-insecure-tls]"},
		{name: "disabled insecure", spec: MetricsServer{Enabled: false, KubeletInsecureTLS: true},
			want: "enabled: false\nargs: [--kubelet-insecure-tls]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config, err := Parse(example(t))
			if err != nil {
				t.Fatal(err)
			}
			worker := Worker{MetricsServer: tt.spec}
			rendered, err := workerValues(*config, worker, releaseFixture())
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := yaml.Unmarshal(rendered, &values); err != nil {
				t.Fatal(err)
			}
			var want any
			if err := yaml.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(values["metrics-server"], want) {
				t.Fatalf("metrics-server values = %#v, want %#v", values["metrics-server"], want)
			}
		})
	}
}
