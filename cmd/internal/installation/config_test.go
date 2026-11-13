package installation

import (
	"os"
	"strings"
	"testing"
)

func example(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../../examples/installation/installation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseExample(t *testing.T) {
	cfg, err := Parse(example(t))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlPlane.Auth.SignupMode != "closed" || len(cfg.Workers) != 1 {
		t.Fatalf("unexpected installation: %#v", cfg)
	}
	if cfg.ControlPlane.Auth.ClientID != "loco" {
		t.Fatal("example must select the public client shared by dashboard and operator sign-in")
	}
}

func TestRejectInvalidConfigurationWithoutValues(t *testing.T) {
	const canary = "credential-canary-do-not-print"
	valid := string(example(t))
	tests := map[string]string{
		"missing boolean": strings.Replace(
			valid, "metricsServer: {enabled: true, kubeletInsecureTLS: false}", "metricsServer: {}", 1,
		),
		"null object":  strings.Replace(valid, "builds: {privateEgress: []}", "builds: null", 1),
		"unknown role": strings.Replace(valid, "role: all", "role: everything", 1),
		"wrong scalar": strings.Replace(valid, "clientId: loco", "clientId: 123", 1),
		"bootstrap remote": strings.Replace(
			valid,
			"http://127.0.0.1:8400/callback",
			"http://"+canary+".example.com:8400/callback",
			1,
		),
		"bootstrap dynamic port": strings.Replace(valid, ":8400/callback", ":0/callback", 1),
		"registry port": strings.Replace(
			valid,
			"endpoint: https://registry.example.com",
			"endpoint: https://registry.example.com:bad",
			1,
		),
		"registry port range": strings.Replace(
			valid,
			"endpoint: https://registry.example.com",
			"endpoint: https://registry.example.com:65536",
			1,
		),
		"secret name": strings.Replace(valid, "name: loco-inputs", "name: invalid/name", 1),
		"secret key":  strings.Replace(valid, "key: database-url", "key: invalid/key", 1),
		"registry path": strings.Replace(
			valid,
			"https://registry.example.com",
			"https://registry.example.com/private",
			1,
		),
		"registry userinfo": strings.Replace(
			valid,
			"https://registry.example.com",
			"https://"+canary+"@registry.example.com",
			1,
		),
		"unknown":        valid + "password: " + canary + "\n",
		"duplicate":      valid + "name: " + canary + "\n",
		"extra document": valid + "---\npassword: " + canary + "\n",
		"syntax":         valid + "password: [" + canary + "\n",
		"inline database": strings.Replace(
			valid,
			"secretRef: {name: loco-inputs, key: database-url}",
			"url: "+canary,
			1,
		),
		"URL userinfo": strings.Replace(
			valid,
			"https://api.example.com",
			"https://"+canary+"@api.example.com",
			1,
		),
		"URL query": strings.Replace(
			valid,
			"https://api.example.com",
			"https://api.example.com?token="+canary,
			1,
		),
		"missing secret key":     strings.Replace(valid, ", key: database-url", "", 1),
		"unsupported version":    strings.Replace(valid, "version: 1", "version: "+canary, 1),
		"external OpenBao":       strings.Replace(valid, "managed: true", "managed: false", 1),
		"disabled observability": strings.Replace(valid, "enabled: true", "enabled: false", 1),
		"invalid domain":         strings.Replace(valid, "staging.apps.example.com", canary+"/bad", 1),
		"unknown issuer":         strings.Replace(valid, "kind: ClusterIssuer", "kind: "+canary, 1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(data))
			if err == nil {
				t.Fatal("accepted invalid configuration")
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("error disclosed input: %v", err)
			}
		})
	}
}

func TestDuplicateWorkers(t *testing.T) {
	data := example(t)
	start := strings.Index(string(data), "  - name:")
	data = append(data, data[start:]...)
	if _, err := Parse(data); err == nil {
		t.Fatal("accepted duplicate workers")
	}
}

func TestLoadReadFailureDoesNotDisclosePath(t *testing.T) {
	_, err := Load("/credential-canary-do-not-print/missing.yaml")
	if err == nil || strings.Contains(err.Error(), "credential-canary") {
		t.Fatalf("unsafe read error: %v", err)
	}
}

func TestEmptyWorkersAndAppsOnlyRole(t *testing.T) {
	valid := string(example(t))
	if _, err := Parse([]byte(strings.Replace(valid, "role: all", "role: apps", 1))); err != nil {
		t.Fatal(err)
	}
	prefix, _, _ := strings.Cut(valid, "workers:\n")
	if _, err := Parse([]byte(prefix + "workers: []\n")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPrivateEgress(t *testing.T) {
	valid := string(example(t))
	data := strings.Replace(
		valid,
		"privateEgress: []",
		"privateEgress: [{cidr: 10.2.3.4/32, port: 5000}, {cidr: fd00::/64, port: 8080}]",
		1,
	)
	config, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Workers[0].Builds.PrivateEgress) != 2 {
		t.Fatal("private destinations were not parsed")
	}
	for name, value := range map[string]string{
		"missing cidr": "{port: 5000}",
		"bad prefix":   "{cidr: invalid-canary, port: 5000}",
		"host bits":    "{cidr: 10.2.3.4/24, port: 5000}",
		"missing port": "{cidr: 10.2.3.4/32}",
		"zero port":    "{cidr: 10.2.3.4/32, port: 0}",
		"high port":    "{cidr: 10.2.3.4/32, port: 65536}",
		"text port":    "{cidr: 10.2.3.4/32, port: invalid-canary}",
		"duplicate":    "{cidr: 10.2.3.4/32, port: 5000}, {cidr: 10.2.3.4/32, port: 5000}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(strings.Replace(valid, "privateEgress: []", "privateEgress: ["+value+"]", 1)))
			if err == nil || strings.Contains(err.Error(), "invalid-canary") {
				t.Fatalf("unsafe or missing validation: %v", err)
			}
		})
	}
}

func TestKubernetesTargetsRequireClusterUID(t *testing.T) {
	valid := string(example(t))
	for _, name := range []string{"control-plane-cluster-uid", "worker-cluster-uid"} {
		data := strings.Replace(valid, "clusterUID: "+name, "clusterUID: ''", 1)
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatal("target without external cluster identity was accepted")
		}
	}
	config, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if config.ControlPlane.Kubernetes.ClusterUID != "control-plane-cluster-uid" {
		t.Fatal("cluster UID was not preserved")
	}
}

func TestWorkerDefinitionsRequireDistinctPhysicalClusters(t *testing.T) {
	data := string(example(t))
	_, worker, found := strings.Cut(data, "workers:\n")
	if !found {
		t.Fatal("example has no workers")
	}
	additional := strings.NewReplacer("staging-us", "other-worker", "namespace: loco-system", "namespace: other-worker").
		Replace(worker)
	_, err := Parse([]byte(data + additional))
	if err == nil {
		t.Fatal("two registrations on one physical worker cluster were accepted")
	}
	if !strings.Contains(err.Error(), "workers[1].kubernetes.clusterUID") ||
		strings.Contains(err.Error(), "worker-cluster-uid") {
		t.Fatalf("unsafe or imprecise target error: %v", err)
	}
}

func TestConfigurationVersion(t *testing.T) {
	valid := string(example(t))
	for _, tc := range []struct {
		version string
		valid   bool
	}{
		{"1", true},
		{"0", false},
		{"2", false},
		{`"1"`, false},
		{"1.0", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			data := strings.Replace(valid, "version: 1", "version: "+tc.version, 1)
			cfg, err := Parse([]byte(data))
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %v, error = %v", tc.valid, err)
			}
			if err == nil && any(cfg.Version) != 1 {
				t.Fatalf("version = %#v, want integer 1", cfg.Version)
			}
		})
	}
}

func TestWorkerMetricsServerOptions(t *testing.T) {
	for _, tt := range []struct {
		options string
		valid   bool
	}{
		{options: "{enabled: true, kubeletInsecureTLS: false}", valid: true},
		{options: "{enabled: false, kubeletInsecureTLS: false}", valid: true},
		{options: "{enabled: true, kubeletInsecureTLS: true}", valid: true},
		{options: "{enabled: false, kubeletInsecureTLS: true}", valid: true},
		{options: ""}, {options: "{}"}, {options: "null"}, {options: "false"},
		{options: "{enabled: true}"}, {options: "{kubeletInsecureTLS: false}"},
		{options: "{enabled: null, kubeletInsecureTLS: false}"},
		{options: "{enabled: 'false', kubeletInsecureTLS: false}"},
		{options: "{enabled: 1, kubeletInsecureTLS: false}"},
		{options: "{enabled: true, kubeletInsecureTLS: null}"},
		{options: "{enabled: true, kubeletInsecureTLS: 'true'}"},
		{options: "{enabled: true, kubeletInsecureTLS: false, unknown: true}"},
		{options: "{enabled: true, enabled: false, kubeletInsecureTLS: false}"},
	} {
		t.Run(tt.options, func(t *testing.T) {
			line := ""
			if tt.options != "" {
				line = "    metricsServer: " + tt.options + "\n"
			}
			data := strings.Replace(string(example(t)),
				"    metricsServer: {enabled: true, kubeletInsecureTLS: false}\n", line, 1)
			_, err := Parse([]byte(data))
			if (err == nil) != tt.valid {
				t.Fatalf("valid = %t, error = %v", tt.valid, err)
			}
		})
	}
}
