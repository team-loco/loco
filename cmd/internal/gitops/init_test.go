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

const (
	controlPlaneCluster = "control-plane"
	testAdmin           = "admin@test.local"
	testDomain          = "apps.test"
)

// testOptions writes env as the API environment file and returns init options for a fresh output.
func testOptions(t *testing.T, env string) InitOptions {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "api.env")
	if err := os.WriteFile(path, []byte(env), 0600); err != nil {
		t.Fatal(err)
	}
	return InitOptions{
		Output:     filepath.Join(root, "fleet"),
		API:        "https://api.test",
		Web:        "https://web.test",
		Domain:     testDomain,
		Region:     "test-east",
		Admin:      testAdmin,
		Bundle:     "oci://registry.test/platform@sha256:" + strings.Repeat("a", 64),
		APIEnvFile: path,
	}
}

func TestInitWritesFreshSecretsAndBothReconciliationLayers(t *testing.T) {
	t.Parallel()
	options := testOptions(t, "DATABASE_URL=postgres://fixture\nAUTH_ISSUERS=[]\n")
	if err := Init(options); err != nil {
		t.Fatal(err)
	}
	sealFile := filepath.Join(options.Output, "clusters", controlPlaneCluster, "config", "openbao-seal.yaml")
	seal := readSecret(t, sealFile)
	key, err := base64.StdEncoding.DecodeString(seal["key"])
	if err != nil || len(key) != 32 {
		t.Fatalf("invalid seal key length: %d, %v", len(key), err)
	}
	api := readSecret(t, filepath.Join(options.Output, "clusters", controlPlaneCluster, "config", "api-env.yaml"))
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
	for _, values := range []string{
		filepath.Join("control-plane", "values", "loco-control-plane.yaml"),
		filepath.Join("worker-staging", "values", "loco-worker.yaml"),
	} {
		data, readErr := os.ReadFile(filepath.Join(options.Output, "clusters", values))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var parsed struct {
			CertManager struct {
				Issuer struct {
					Solvers *[]any `json:"solvers"`
				} `json:"issuer"`
			} `json:"certManager"`
		}
		if readErr = yaml.Unmarshal(data, &parsed); readErr != nil {
			t.Fatal(readErr)
		}
		if solvers := parsed.CertManager.Issuer.Solvers; solvers == nil || len(*solvers) != 0 {
			t.Fatalf("%s scaffolds ACME solvers %v, want an empty list the operator fills", values, solvers)
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
	options := InitOptions{
		Output:        filepath.Join(root, "fleet"),
		API:           "http://api.test:8000",
		Web:           "http://web.test:5173",
		Domain:        testDomain,
		Region:        "local",
		Admin:         testAdmin,
		Bundle:        "oci://registry.test/platform@sha256:" + strings.Repeat("a", 64),
		APIEnvFile:    env,
		SingleCluster: true,
		Name:          "combined",
	}
	if err := Init(options); err != nil {
		t.Fatal(err)
	}
	clusters, err := os.ReadDir(filepath.Join(options.Output, "clusters"))
	if err != nil || len(clusters) != 1 || clusters[0].Name() != "combined" {
		t.Fatalf("clusters: %v, %v", clusters, err)
	}
	for _, release := range []string{"loco-control-plane", "loco-worker"} {
		if _, err = os.Stat(
			filepath.Join(options.Output, "clusters", "combined", "values", release+".yaml"),
		); err != nil {
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

func TestInitPostgresWaitsForTheOperator(t *testing.T) {
	t.Parallel()
	for _, single := range []bool{false, true} {
		options := testOptions(t, "AUTH_ISSUERS=[]\n")
		options.SingleCluster, options.Name, options.Postgres = single, "local", true
		if err := Init(options); err != nil {
			t.Fatal(err)
		}
		base := "control-plane"
		if single {
			base = "all"
		}
		data, err := os.ReadFile(filepath.Join(options.Output, "bases", base, "loco.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		paths := map[string]string{}
		var config []string
		for document := range strings.SplitSeq(string(data), "\n---\n") {
			var kustomization struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Spec struct {
					Path      string `json:"path"`
					DependsOn []struct {
						Name string `json:"name"`
					} `json:"dependsOn"`
				} `json:"spec"`
			}
			if err = yaml.Unmarshal([]byte(document), &kustomization); err != nil {
				t.Fatal(err)
			}
			paths[kustomization.Metadata.Name] = kustomization.Spec.Path
			if kustomization.Metadata.Name == "loco-cluster-config" {
				for _, dependency := range kustomization.Spec.DependsOn {
					config = append(config, dependency.Name)
				}
			}
		}
		if paths["loco-postgres"] != "./postgres-operator" {
			t.Errorf("single=%v: base applies %v, want loco-postgres at ./postgres-operator", single, paths)
		}
		if strings.Join(config, " ") != "loco-namespaces loco-postgres-operator" {
			t.Errorf("single=%v: loco-cluster-config depends on %v", single, config)
		}
		values, err := os.ReadFile(filepath.Join(options.Output, "bases", "control-plane", "values", "values.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			Postgres struct {
				Enabled bool `json:"enabled"`
			} `json:"postgres"`
		}
		if err = yaml.Unmarshal(values, &parsed); err != nil || !parsed.Postgres.Enabled {
			t.Errorf("single=%v: control-plane values do not enable postgres: %v", single, err)
		}
	}
}

func TestInitRegistryPointsBothReleasesAtTheBundledRegistry(t *testing.T) {
	t.Parallel()
	options := testOptions(t, "AUTH_ISSUERS=[]\n")
	options.Registry = "images.test"
	if err := Init(options); err != nil {
		t.Fatal(err)
	}
	var control struct {
		Registry struct {
			Enabled  bool   `json:"enabled"`
			Hostname string `json:"hostname"`
		} `json:"registry"`
		API struct {
			Registry struct {
				Service string `json:"service"`
			} `json:"registry"`
		} `json:"api"`
	}
	readValues(t, filepath.Join(options.Output, "clusters", controlPlaneCluster, "values", "loco-control-plane.yaml"),
		&control)
	if !control.Registry.Enabled || control.Registry.Hostname != options.Registry ||
		control.API.Registry.Service != options.Registry {
		t.Errorf("control-plane values: %+v", control)
	}
	var worker struct {
		Env map[string]string `json:"env"`
	}
	readValues(t, filepath.Join(options.Output, "clusters", "worker-staging", "values", "loco-worker.yaml"), &worker)
	if worker.Env["LOCO_REGISTRY_HOST"] != options.Registry {
		t.Errorf("worker LOCO_REGISTRY_HOST is %q, want %q", worker.Env["LOCO_REGISTRY_HOST"], options.Registry)
	}
}

func readValues(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}
