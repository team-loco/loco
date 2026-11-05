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
}

func TestRejectInvalidConfigurationWithoutValues(t *testing.T) {
	const canary = "credential-canary-do-not-print"
	valid := string(example(t))
	tests := map[string]string{
		"missing boolean": strings.Replace(valid, "builds: {enabled: true, privateEgress: []}", "builds: {}", 1),
		"null object":     strings.Replace(valid, "builds: {enabled: true, privateEgress: []}", "builds: null", 1),
		"wrong scalar":    strings.Replace(valid, "audience: loco", "audience: 123", 1),
		"bootstrap remote": strings.Replace(
			valid,
			"http://127.0.0.1:8400/callback",
			"http://"+canary+".example.com:8400/callback",
			1,
		),
		"bootstrap dynamic port": strings.Replace(valid, ":8400/callback", ":0/callback", 1),
		"bootstrap audience":     strings.Replace(valid, "bootstrapClientId: loco", "bootstrapClientId: "+canary, 1),
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
		"unsupported version":    strings.Replace(valid, "loco.build/admin/v1alpha1", canary, 1),
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

func TestEmptyWorkersAndDisabledBuilds(t *testing.T) {
	valid := string(example(t))
	if _, err := Parse(
		[]byte(
			strings.Replace(
				valid,
				"builds: {enabled: true, privateEgress: []}",
				"builds: {enabled: false, privateEgress: []}",
				1,
			),
		),
	); err != nil {
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
