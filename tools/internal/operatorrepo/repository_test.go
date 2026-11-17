package operatorrepo

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestMergePreservesNestedValuesAndOverridesLists(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(
		path,
		[]byte(
			"api:\n  env: {WEB_URL: https://console.test, CORS_ALLOWED_ORIGINS: https://console.test}\n"+
				"  systemAdmins: [old@test]\n",
		),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	if err := Merge(
		path,
		[]byte(
			"api:\n  env: {WEB_URL: http://localhost:5173}\n  systemAdmins: [dev@test]\n  registry: {auth: static}\n",
		),
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err = yaml.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{
		"api": map[string]any{
			"env": map[string]any{
				"WEB_URL":              "http://localhost:5173",
				"CORS_ALLOWED_ORIGINS": "https://console.test",
			},
			"systemAdmins": []any{"dev@test"},
			"registry":     map[string]any{"auth": "static"},
		},
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("merged values: %#v", actual)
	}
}

func TestConfigureSelectsDigestAndKeepsConfigOnRepositorySource(t *testing.T) {
	t.Parallel()
	repo := Repository{Path: t.TempDir(), Cluster: "local", Source: "fixture-repo"}
	for path, data := range map[string]string{
		"releases/control-plane.yaml": "spec: {url: oci://example.test/platform, ref: {tag: stable}}\n",
		"bases/all/loco.yaml": "metadata: {name: loco}\nspec: {path: ./all}\n---\n" +
			"metadata: {name: loco-cluster-config}\nspec: {path: ./}\n---\n",
	} {
		destination := filepath.Join(repo.Path, path)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []string{"./control-plane", "./all"} {
		if err := repo.Configure("registry.test:5000/platform@sha256:"+strings.Repeat("a", 64), entry); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(repo.Path, "bases", "all", "loco.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "path: "+entry) || !strings.Contains(string(data), "name: fixture-repo") {
			t.Fatalf("repository wiring: %s", data)
		}
	}
	data, err := os.ReadFile(filepath.Join(repo.Path, "releases", "control-plane.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Spec struct {
			URL      string            `json:"url"`
			Ref      map[string]string `json:"ref"`
			Insecure bool              `json:"insecure"`
		} `json:"spec"`
	}
	if err = yaml.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	if source.Spec.URL != "oci://registry.test:5000/platform" ||
		source.Spec.Ref["digest"] != "sha256:"+strings.Repeat("a", 64) ||
		!source.Spec.Insecure {
		t.Fatalf("bundle selection: %+v", source.Spec)
	}
	for _, reference := range []string{
		"registry.test:5000/repo@sha256:" + strings.Repeat("b", 64), "oci://registry.test:5000/repo:local",
	} {
		bootstrap, buildErr := repo.Bootstrap(reference)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if !strings.Contains(string(bootstrap), "path: ./clusters/local") ||
			!strings.Contains(string(bootstrap), "oci://registry.test:5000/repo") {
			t.Fatalf("bootstrap: %s", bootstrap)
		}
	}
}
