package gitops

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func readSecret(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var secret struct {
		StringData map[string]string `json:"stringData"`
	}
	if err = yaml.Unmarshal(data, &secret); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("secret mode: %v", info.Mode())
	}
	return secret.StringData
}

func TestInitWritesFreshSecretsAndBothReconciliationLayers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := filepath.Join(root, "api.env")
	if err := os.WriteFile(env, []byte("DATABASE_URL=postgres://fixture\nAUTH_ISSUERS=[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	options := InitOptions{
		Output:     filepath.Join(root, "fleet"),
		API:        "https://api.test",
		Web:        "https://web.test",
		Domain:     "apps.test",
		Region:     "test-east",
		Admin:      "admin@test.local",
		Bundle:     "oci://registry.test/platform@sha256:" + strings.Repeat("a", 64),
		APIEnvFile: env,
	}
	if err := Init(options); err != nil {
		t.Fatal(err)
	}
	sealFile := filepath.Join(options.Output, "clusters", "control-plane", "config", "openbao-seal.yaml")
	seal := readSecret(t, sealFile)
	key, err := base64.StdEncoding.DecodeString(seal["key"])
	if err != nil || len(key) != 32 {
		t.Fatalf("invalid seal key length: %d, %v", len(key), err)
	}
	api := readSecret(t, filepath.Join(options.Output, "clusters", "control-plane", "config", "api-env.yaml"))
	if api["DATABASE_URL"] != "postgres://fixture" {
		t.Fatal("API environment differs from input")
	}
	readme, err := os.ReadFile(filepath.Join(options.Output, "README.md"))
	if err != nil || !strings.Contains(string(readme), "encrypt them before committing") {
		t.Fatalf("missing encryption guidance: %v", err)
	}
	for _, role := range []string{"control-plane", "worker"} {
		data, readErr := os.ReadFile(
			filepath.Join(
				options.Output,
				"bases",
				role,
				"loco.yaml",
			),
		)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Count(string(data), "kind: Kustomization") != 2 {
			t.Fatal("scaffold omitted a reconciliation layer")
		}
	}
	cohort, err := os.ReadFile(filepath.Join(options.Output, "releases", "workers-staging.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cohort), "semver:") {
		t.Fatal("staging lost automatic stable release selection")
	}
}

func TestInitSingleCluster(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	env := filepath.Join(root, "api.env")
	if err := os.WriteFile(env, []byte("DATABASE_URL=postgres://fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	options := InitOptions{Output: filepath.Join(root, "fleet"), API: "http://api.test:8000", Web: "http://web.test:5173", Domain: "apps.test", Region: "local", Admin: "admin@test.local", Bundle: "oci://registry.test/platform@sha256:" + strings.Repeat("a", 64), APIEnvFile: env, SingleCluster: true, Name: "combined"}
	if err := Init(options); err != nil {
		t.Fatal(err)
	}
	clusters, err := os.ReadDir(filepath.Join(options.Output, "clusters"))
	if err != nil || len(clusters) != 1 || clusters[0].Name() != "combined" {
		t.Fatalf("clusters: %v, %v", clusters, err)
	}
	for _, release := range []string{"loco-control-plane", "loco-worker"} {
		if _, err = os.Stat(filepath.Join(options.Output, "clusters", "combined", "values", release+".yaml")); err != nil {
			t.Fatal(err)
		}
	}
	readSecret(t, filepath.Join(options.Output, "clusters", "combined", "config", "openbao-seal.yaml"))
	base, err := os.ReadFile(filepath.Join(options.Output, "bases", "all", "loco.yaml"))
	if err != nil || !strings.Contains(string(base), "path: ./all") {
		t.Fatalf("combined entry: %s, %v", base, err)
	}
	cluster, err := os.ReadFile(filepath.Join(options.Output, "clusters", "combined", "kustomization.yaml"))
	if err != nil || !strings.Contains(string(cluster), "./clusters/combined/config") {
		t.Fatalf("config path: %s, %v", cluster, err)
	}
	options.Output = filepath.Join(root, "invalid")
	options.Name = "../outside"
	if err = Init(options); err == nil {
		t.Fatal("accepted an unsafe cluster name")
	}
}
