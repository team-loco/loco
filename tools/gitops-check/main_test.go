package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestCRDSchemasValidateUnknownFieldsAndPreserveExplicitMaps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  group: example.test
  names: {kind: Settings}
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                values:
                  type: object
                  x-kubernetes-preserve-unknown-fields: true
                  properties: {known: {type: string}}
`)
	if err := writeSchemas(dir, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "example.test", "settings_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatal("schema accepts unknown root properties")
	}
	spec := object(t, object(t, schema["properties"])["spec"])
	if spec["additionalProperties"] != false {
		t.Fatal("schema accepts unknown spec fields")
	}
	values := object(t, object(t, spec["properties"])["values"])
	if _, constrained := values["additionalProperties"]; constrained {
		t.Fatal("schema restricts an explicit unstructured map")
	}
}

func object(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected an object, got %T", value)
	}
	return object
}

func TestValidationRejectsAnInvalidProductionCohort(t *testing.T) {
	root := t.TempDir()
	example := filepath.Join(root, "examples", "gitops")
	if err := os.MkdirAll(filepath.Dir(example), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(example, os.DirFS("../../examples/gitops")); err != nil {
		t.Fatal(err)
	}
	charts, err := filepath.Abs("../../charts")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(charts, filepath.Join(root, "charts")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(example, "releases", "workers-production.yaml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err = yaml.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	object(t, source["spec"])["interval"] = true
	data, err = yaml.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := check(t.Context(), root); err == nil || !strings.Contains(err.Error(), "kubeconform") {
		t.Fatalf("invalid production cohort: %v, want schema rejection", err)
	}
}

func TestClusterSelectsExactlyOneCohort(t *testing.T) {
	t.Parallel()
	const workerBase = "../../bases/worker"
	for _, tc := range []struct {
		name      string
		resources []string
		valid     bool
	}{
		{name: "staging", resources: []string{"../../releases/workers-staging.yaml", workerBase}, valid: true},
		{name: "production",
			resources: []string{"../../releases/workers-production.yaml", workerBase}, valid: true},
		{name: "inherited cohort", resources: []string{workerBase}},
		{name: "two cohorts", resources: []string{
			"../../releases/workers-staging.yaml", "../../releases/workers-production.yaml", workerBase,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			cluster := filepath.Join(repo, "clusters", "worker")
			if err := os.MkdirAll(cluster, 0700); err != nil {
				t.Fatal(err)
			}
			data, err := yaml.Marshal(map[string]any{"resources": tc.resources})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(cluster, "kustomization.yaml"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = checkCohort(repo, cluster); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
