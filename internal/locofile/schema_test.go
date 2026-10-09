package locofile

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const schemaPath = "schemas/loco.v1.json"

type schemaProperty struct {
	Type                 string          `json:"type"`
	Ref                  string          `json:"$ref"`
	Enum                 []int           `json:"enum"`
	Default              string          `json:"default"`
	Description          string          `json:"description"`
	AdditionalProperties *schemaProperty `json:"additionalProperties"`
}

type schemaObject struct {
	Type                 string                    `json:"type"`
	Required             []string                  `json:"required"`
	AdditionalProperties bool                      `json:"additionalProperties"`
	Properties           map[string]schemaProperty `json:"properties"`
}

type schemaFile struct {
	ID   string                  `json:"$id"`
	Defs map[string]schemaObject `json:"$defs"`
	schemaObject
}

func loadSchema(t *testing.T) schemaFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", schemaPath))
	require.NoError(t, err)
	var schema schemaFile
	require.NoError(t, json.Unmarshal(data, &schema))
	return schema
}

func jsonFields(t *testing.T, goType reflect.Type) map[string]reflect.Type {
	t.Helper()
	fields := map[string]reflect.Type{}
	for field := range goType.Fields() {
		tag, tagged := field.Tag.Lookup("json")
		if !tagged {
			require.False(t, field.IsExported(), "%s.%s has no json tag", goType.Name(), field.Name)
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		fields[name] = field.Type
	}
	return fields
}

func assertObjectMatchesType(t *testing.T, object schemaObject, goType reflect.Type) {
	t.Helper()
	assert.Equal(t, "object", object.Type, goType.Name())
	assert.False(t, object.AdditionalProperties, "%s allows additional properties", goType.Name())

	fields := jsonFields(t, goType)
	assert.ElementsMatch(t, keys(fields), keys(object.Properties), "%s properties", goType.Name())

	var required []string
	for name, fieldType := range fields {
		property, found := object.Properties[name]
		if !found {
			continue
		}
		assert.NotEmpty(t, property.Description, "%s.%s has no description", goType.Name(), name)
		assertPropertyMatchesType(t, goType.Name()+"."+name, property, fieldType)
		tag := fieldByJSONName(goType, name).Tag.Get("json")
		if !strings.Contains(tag, ",omitempty") {
			required = append(required, name)
		}
	}
	assert.ElementsMatch(t, required, object.Required, "%s required", goType.Name())
}

func assertPropertyMatchesType(t *testing.T, path string, property schemaProperty, fieldType reflect.Type) {
	t.Helper()
	if fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	switch fieldType.Kind() {
	case reflect.Struct:
		assert.Equal(t, "#/$defs/"+fieldType.Name(), property.Ref, path)
	case reflect.Map:
		assert.Equal(t, "object", property.Type, path)
		require.NotNil(t, property.AdditionalProperties, path)
		assertPropertyMatchesType(t, path+"[]", *property.AdditionalProperties, fieldType.Elem())
	case reflect.Slice:
		assert.Equal(t, "array", property.Type, path)
	case reflect.String:
		assert.Equal(t, "string", property.Type, path)
	case reflect.Int:
		assert.Equal(t, "integer", property.Type, path)
	case reflect.Int32:
		assert.Equal(t, "integer", property.Type, path)
	case reflect.Bool:
		assert.Equal(t, "boolean", property.Type, path)
	default:
		t.Fatalf("%s: unhandled Go kind %s", path, fieldType.Kind())
	}
}

func fieldByJSONName(goType reflect.Type, name string) reflect.StructField {
	for field := range goType.Fields() {
		tag := field.Tag.Get("json")
		tagName, _, _ := strings.Cut(tag, ",")
		if tagName == name {
			return field
		}
	}
	return reflect.StructField{}
}

func keys[V any](m map[string]V) []string {
	result := make([]string, 0, len(m))
	for key := range m {
		result = append(result, key)
	}
	return result
}

func TestSchemaMatchesTypes(t *testing.T) {
	schema := loadSchema(t)
	assert.Equal(t, "https://loco.build/schemas/loco.v1.json", schema.ID)
	assertObjectMatchesType(t, schema.schemaObject, reflect.TypeFor[File]())

	defs := []reflect.Type{
		reflect.TypeFor[Service](),
		reflect.TypeFor[Override](),
		reflect.TypeFor[Health](),
		reflect.TypeFor[Routing](),
		reflect.TypeFor[Region](),
		reflect.TypeFor[Replicas](),
		reflect.TypeFor[Autoscaling](),
	}
	defNames := make([]string, 0, len(defs))
	for _, goType := range defs {
		defNames = append(defNames, goType.Name())
		object, found := schema.Defs[goType.Name()]
		require.True(t, found, "$defs.%s is missing", goType.Name())
		assertObjectMatchesType(t, object, goType)
	}
	assert.ElementsMatch(t, defNames, keys(schema.Defs))
}

func TestSchemaConstraints(t *testing.T) {
	schema := loadSchema(t)
	assert.Equal(t, []int{Version}, schema.Properties[keyVersion].Enum)
	assert.Equal(t, "Dockerfile", schema.Defs["Service"].Properties["dockerfile"].Default)
	assert.Equal(t, ".", schema.Defs["Service"].Properties["context"].Default)
}
