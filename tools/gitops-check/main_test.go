package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidationKeepsSecretDataAndRemovesEncryptionMetadata(t *testing.T) {
	t.Parallel()
	output, err := plainResources(
		[]byte("apiVersion: v1\nkind: Secret\nstringData: {token: 'ENC[ciphertext]'}\nsops: {version: test}\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := documents(output, func(resource map[string]any) error {
		if _, exists := resource["sops"]; exists {
			t.Fatal("encryption metadata sent to resource schema validation")
		}
		if object(t, resource["stringData"])["token"] != "ENC[ciphertext]" {
			t.Fatal("validation changed secret data")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

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
