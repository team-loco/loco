package installation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func releaseFixture() Manifest {
	manifest := Manifest{
		ConfigVersion: ConfigVersion,
		Version:       "0.7.0",
		CoreCommit:    strings.Repeat("a", 40),
		Artifacts:     map[string]string{},
		Images:        map[string]string{},
	}
	for _, key := range []string{
		"loco-control-plane", "loco-worker", "cert-manager-chart",
		"loco-applications-crd", "loco-builds-crd", "loco-gateway-crd",
	} {
		manifest.Artifacts[key] = "ghcr.io/example/" + key + "@sha256:" + strings.Repeat("b", 64)
	}
	for _, key := range []string{"api", "ui", "obs-proxy", "agent", "controller", "builder"} {
		manifest.Images[key] = "ghcr.io/example/" + key + "@sha256:" + strings.Repeat("c", 64)
	}
	return manifest
}

func TestReleaseRequiresCompatibleDigestPins(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Manifest)
		valid  bool
	}{
		{"valid", func(*Manifest) {}, true},
		{"unsupported-version", func(m *Manifest) { m.Version = "latest" }, false},
		{"config-version", func(m *Manifest) { m.ConfigVersion = "future" }, false},
		{"mutable-image", func(m *Manifest) { m.Images["api"] = "ghcr.io/example/api:latest" }, false},
		{"missing-CRDs", func(m *Manifest) { delete(m.Artifacts, "loco-gateway-crd") }, false},
		{"credential-url", func(m *Manifest) {
			m.Artifacts["loco-worker"] = "https://user:credential-canary@example.com/chart"
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := releaseFixture()
			test.change(&manifest)
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "release.json")
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = LoadManifest(path)
			if test.valid && err != nil {
				t.Fatal(err)
			}
			if !test.valid && !errors.Is(err, errInvalidRelease) {
				t.Fatalf("got %v, want invalid release", err)
			}
			if err != nil && strings.Contains(err.Error(), "credential-canary") {
				t.Fatal("release error exposed credential")
			}
		})
	}
}

func TestWorkerSuppliesPinnedGatewayPrerequisite(t *testing.T) {
	worker, err := packageArchive(map[string][]byte{
		"loco-worker/Chart.yaml":                                    []byte("name: loco-worker\n"),
		"loco-worker/charts/gateway-helm/Chart.yaml":                []byte("name: gateway-helm\nversion: 1.2.3\n"),
		"loco-worker/charts/gateway-helm/templates/deployment.yaml": []byte("kind: Deployment\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := gatewayChart(worker)
	if err != nil {
		t.Fatal(err)
	}
	files, err := archiveFiles(gateway)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["gateway-helm/templates/deployment.yaml"]) != "kind: Deployment\n" {
		t.Fatal("gateway deployment missing from prerequisite chart")
	}
}

func TestApplyPreservesRecordedRelease(t *testing.T) {
	manifest := releaseFixture()
	for _, state := range []map[string]string{
		{},
		{"releaseCommit": manifest.CoreCommit, "releaseVersion": manifest.Version},
	} {
		if err := validateRecordedRelease(state, manifest); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []map[string]string{{"releaseCommit": strings.Repeat("d", 40)}, {"releaseVersion": "0.8.0"}} {
		if !errors.Is(validateRecordedRelease(state, manifest), errReleaseChanged) {
			t.Fatal("apply accepted a different release")
		}
	}
}
