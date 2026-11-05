package installation

import (
	"encoding/json/v2"
	"os"
	"reflect"
	"slices"
	"testing"
)

type schema struct {
	Type                 string            `json:"type"`
	Required             []string          `json:"required"`
	Properties           map[string]schema `json:"properties"`
	AdditionalProperties *bool             `json:"additionalProperties"`
	Items                *schema           `json:"items"`
}

func TestSchemaMatchesConfiguration(t *testing.T) {
	data, err := os.ReadFile("../../../schemas/installation.v1alpha1.json")
	if err != nil {
		t.Fatal(err)
	}
	var document schema
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	checkSchema(t, reflect.TypeFor[Config](), document)
}

func checkSchema(t *testing.T, typ reflect.Type, document schema) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Struct:
		if document.Type != "object" || document.AdditionalProperties == nil || *document.AdditionalProperties ||
			len(document.Properties) != typ.NumField() ||
			len(document.Required) != typ.NumField() {
			t.Fatalf("schema diverges from %s", typ)
		}
		for field := range typ.Fields() {
			name := field.Tag.Get("json")
			property, ok := document.Properties[name]
			if !ok || !slices.Contains(document.Required, name) {
				t.Fatalf("schema missing required %s.%s", typ, name)
			}
			checkSchema(t, field.Type, property)
		}
	case reflect.Slice:
		if document.Type != "array" || document.Items == nil {
			t.Fatalf("schema array diverges from %s", typ)
		}
		checkSchema(t, typ.Elem(), *document.Items)
	case reflect.String:
		if document.Type != "string" {
			t.Fatalf("schema string diverges from %s", typ)
		}
	case reflect.Bool:
		if document.Type != "boolean" {
			t.Fatalf("schema boolean diverges from %s", typ)
		}
	}
}
