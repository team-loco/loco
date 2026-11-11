package installation

import (
	"encoding/json/v2"
	"os"
	"reflect"
	"slices"
	"testing"
)

type schema struct {
	Const                any               `json:"const"`
	Type                 string            `json:"type"`
	Required             []string          `json:"required"`
	Properties           map[string]schema `json:"properties"`
	AdditionalProperties *bool             `json:"additionalProperties"`
	Items                *schema           `json:"items"`
}

func TestSchemaMatchesConfiguration(t *testing.T) {
	data, err := os.ReadFile("../../../schemas/installation.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var document schema
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Properties["version"].Const != float64(ConfigVersion) {
		t.Fatal("schema version differs from supported configuration version")
	}
	checkSchema(t, reflect.TypeFor[Config](), document)
}

func checkSchema(t *testing.T, typ reflect.Type, document schema) {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		if document.Type != "object" || document.AdditionalProperties == nil || *document.AdditionalProperties ||
			len(document.Properties) != typ.NumField() {
			t.Fatalf("schema diverges from %s", typ)
		}
		required := 0
		for field := range typ.Fields() {
			name := field.Tag.Get("json")
			property, ok := document.Properties[name]
			optional := field.Type.Kind() == reflect.Pointer
			if !ok || slices.Contains(document.Required, name) == optional {
				t.Fatalf("schema optionality differs for %s.%s", typ, name)
			}
			if !optional {
				required++
			}
			checkSchema(t, field.Type, property)
		}
		if len(document.Required) != required {
			t.Fatalf("schema required fields diverge from %s", typ)
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
	case reflect.Int:
		if document.Type != "integer" {
			t.Fatalf("schema integer diverges from %s", typ)
		}
	case reflect.Int32:
		if document.Type != "integer" {
			t.Fatalf("schema integer diverges from %s", typ)
		}
	case reflect.Bool:
		if document.Type != "boolean" {
			t.Fatalf("schema boolean diverges from %s", typ)
		}
	}
}
