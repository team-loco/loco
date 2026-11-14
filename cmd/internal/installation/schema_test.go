package installation

import (
	"encoding/json/v2"
	"os"
	"reflect"
	"slices"
	"strings"
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
	switch typ.Kind() {
	case reflect.Struct:
		if document.Type != "object" || document.AdditionalProperties == nil || *document.AdditionalProperties ||
			len(document.Properties) != typ.NumField() {
			t.Fatalf("schema diverges from %s", typ)
		}
		required := 0
		for field := range typ.Fields() {
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			property, ok := document.Properties[name]
			if !ok || slices.Contains(document.Required, name) == (options == "omitempty") {
				t.Fatalf("schema diverges from %s.%s", typ, name)
			}
			if options != "omitempty" {
				required++
			}
			checkSchema(t, field.Type, property)
		}
		if len(document.Required) != required {
			t.Fatalf("schema requires unknown fields of %s", typ)
		}
	case reflect.Pointer:
		checkSchema(t, typ.Elem(), document)
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
