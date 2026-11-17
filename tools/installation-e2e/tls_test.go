package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/team-loco/loco/tools/internal/operatorrepo"
	"sigs.k8s.io/yaml"
)

func TestTLSValuesUseGeneratedBlockYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.yaml")
	base, err := os.ReadFile("../../examples/gitops/clusters/control-plane/values/loco-control-plane.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base = []byte(strings.ReplaceAll(string(base), "https://app.example.com", "http://"+fixtureWebHost))
	if err = os.WriteFile(path, base, 0600); err != nil {
		t.Fatal(err)
	}
	overrides, err := os.ReadFile("../../tests/e2e/fixtures/installation/values/loco-control-plane.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = operatorrepo.Merge(path, overrides); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := tlsControlPlaneValues(string(source))
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		CertManager struct {
			Enabled bool
			Issuer  struct {
				Create     bool
				Name, Kind string
			}
		}
		Gateway struct{ TLS struct{ MinVersion string } }
		API     struct {
			Env      map[string]string
			Registry struct{ Auth string }
		}
	}
	if err = yaml.Unmarshal([]byte(updated), &actual); err != nil {
		t.Fatal(err)
	}
	if !actual.CertManager.Enabled || actual.CertManager.Issuer.Create ||
		actual.CertManager.Issuer.Name != "installation-ca" || actual.CertManager.Issuer.Kind != "ClusterIssuer" ||
		actual.Gateway.TLS.MinVersion != "1.3" {
		t.Fatalf("TLS values = %+v", actual)
	}
	for _, key := range []string{"WEB_URL", "CORS_ALLOWED_ORIGINS"} {
		if actual.API.Env[key] != "https://"+fixtureWebHost {
			t.Errorf("%s = %q", key, actual.API.Env[key])
		}
	}
	if actual.API.Registry.Auth != "static" || actual.API.Env["LOCO_REGISTRY_HOST"] != registryHost {
		t.Fatal("TLS update changed unrelated fixture values")
	}
}

func TestTLSValuesRejectMissingKeys(t *testing.T) {
	for _, path := range []string{
		"certManager.enabled", "certManager.issuer", "api.env.WEB_URL", "api.env.CORS_ALLOWED_ORIGINS",
	} {
		t.Run(path, func(t *testing.T) {
			values := map[string]any{
				"certManager": map[string]any{"enabled": false, "issuer": map[string]any{"email": fixtureEmail}},
				"api": map[string]any{"env": map[string]any{
					"WEB_URL": "http://" + fixtureWebHost, "CORS_ALLOWED_ORIGINS": "http://" + fixtureWebHost,
				}},
			}
			parent := values
			keys := strings.Split(path, ".")
			for _, key := range keys[:len(keys)-1] {
				nested, exists := parent[key].(map[string]any)
				if !exists {
					t.Fatalf("invalid fixture path %s", path)
				}
				parent = nested
			}
			delete(parent, keys[len(keys)-1])
			data, err := yaml.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tlsControlPlaneValues(string(data))
			if !errors.Is(err, errTLSFixtureValues) || !strings.Contains(err.Error(), path) {
				t.Fatalf("missing key %s: %v", path, err)
			}
		})
	}
}
