package locofile

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const (
	schemaPath     = "schemas/loco.v1.json"
	schemaNull     = "null"
	overridePrefix = "Override"
)

const nullOverrideFile = `version: 1
partial: api
services:
  api:
    image: ghcr.io/acme/api:1
    port: 8080
    env: { A: "1", B: "2" }
    domains: [api.example.com]
    health: { path: /healthz, interval: 10 }
    routing: { pathPrefix: /api }
    regions:
      us-east-1:
        cpu: 100m
        memory: 256Mi
        replicas: { min: 1, max: 2 }
        autoscaling: { cpuTarget: 80 }
      eu-west-1: { cpu: 100m, memory: 256Mi, replicas: { min: 1, max: 1 } }
    environments:
      prod:
        domains: null
        env: { B: null, C: "3" }
        routing: null
        health: { interval: null }
        regions:
          us-east-1: { autoscaling: null }
          eu-west-1: null
`

type schemaTypes []string

func (s *schemaTypes) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = schemaTypes{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

type schemaProperty struct {
	Type                 schemaTypes      `json:"type"`
	Ref                  string           `json:"$ref"`
	AnyOf                []schemaProperty `json:"anyOf"`
	Enum                 []int            `json:"enum"`
	Default              string           `json:"default"`
	Description          string           `json:"description"`
	AdditionalProperties *schemaProperty  `json:"additionalProperties"`
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

func readSchema(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", schemaPath))
	require.NoError(t, err)
	return data
}

func loadSchema(t *testing.T) schemaFile {
	t.Helper()
	var schema schemaFile
	require.NoError(t, json.Unmarshal(readSchema(t), &schema))
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

func assertObjectMatchesType(t *testing.T, defName string, object schemaObject, goType reflect.Type, nullable bool) {
	t.Helper()
	assert.Equal(t, "object", object.Type, defName)
	assert.False(t, object.AdditionalProperties, "%s allows additional properties", defName)

	fields := jsonFields(t, goType)
	assert.ElementsMatch(t, keys(fields), keys(object.Properties), "%s properties", defName)

	var required []string
	for name, fieldType := range fields {
		property, found := object.Properties[name]
		if !found {
			continue
		}
		path := defName + "." + name
		assert.NotEmpty(t, property.Description, "%s has no description", path)
		propertyNullable := nullable && name != keyEnabled
		assertPropertyMatchesType(t, path, property, fieldType, propertyNullable)
		tag := fieldByJSONName(goType, name).Tag.Get("json")
		if !strings.Contains(tag, ",omitempty") {
			required = append(required, name)
		}
	}
	assert.ElementsMatch(t, required, object.Required, "%s required", defName)
}

func assertPropertyMatchesType(
	t *testing.T,
	path string,
	property schemaProperty,
	fieldType reflect.Type,
	nullable bool,
) {
	t.Helper()
	if fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	if fieldType.Kind() == reflect.Struct {
		refPrefix := "#/$defs/"
		if nullable {
			refPrefix += overridePrefix
			property = nonNullBranch(t, path, property)
		}
		assert.Equal(t, refPrefix+fieldType.Name(), property.Ref, path)
		return
	}
	types := slices.Clone(property.Type)
	if nullable {
		assert.Contains(t, types, schemaNull, "%s does not accept null", path)
		types = slices.DeleteFunc(types, func(name string) bool { return name == schemaNull })
	} else {
		assert.NotContains(t, types, schemaNull, "%s accepts null", path)
	}
	require.Len(t, types, 1, path)
	switch fieldType.Kind() {
	case reflect.Map:
		assert.Equal(t, "object", types[0], path)
		require.NotNil(t, property.AdditionalProperties, path)
		assertPropertyMatchesType(t, path+"[]", *property.AdditionalProperties, fieldType.Elem(), nullable)
	case reflect.Slice:
		assert.Equal(t, "array", types[0], path)
	case reflect.String:
		assert.Equal(t, "string", types[0], path)
	case reflect.Int:
		assert.Equal(t, "integer", types[0], path)
	case reflect.Int32:
		assert.Equal(t, "integer", types[0], path)
	case reflect.Bool:
		assert.Equal(t, "boolean", types[0], path)
	default:
		t.Fatalf("%s: unhandled Go kind %s", path, fieldType.Kind())
	}
}

func nonNullBranch(t *testing.T, path string, property schemaProperty) schemaProperty {
	t.Helper()
	require.Len(t, property.AnyOf, 2, "%s needs a null branch", path)
	var branch schemaProperty
	hasNull := false
	for _, candidate := range property.AnyOf {
		if slices.Equal(candidate.Type, schemaTypes{schemaNull}) {
			hasNull = true
			continue
		}
		branch = candidate
	}
	assert.True(t, hasNull, "%s does not accept null", path)
	return branch
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
	assertObjectMatchesType(t, "File", schema.schemaObject, reflect.TypeFor[File](), false)

	defs := map[string]reflect.Type{}
	nested := []reflect.Type{
		reflect.TypeFor[Health](),
		reflect.TypeFor[Routing](),
		reflect.TypeFor[Region](),
		reflect.TypeFor[Replicas](),
		reflect.TypeFor[Autoscaling](),
	}
	for _, goType := range nested {
		defs[goType.Name()] = goType
		defs[overridePrefix+goType.Name()] = goType
	}
	defs["Service"] = reflect.TypeFor[Service]()
	defs["Override"] = reflect.TypeFor[Override]()

	for name, goType := range defs {
		object, found := schema.Defs[name]
		require.True(t, found, "$defs.%s is missing", name)
		nullable := strings.HasPrefix(name, overridePrefix)
		assertObjectMatchesType(t, name, object, goType, nullable)
	}
	assert.ElementsMatch(t, keys(defs), keys(schema.Defs))
}

func TestSchemaConstraints(t *testing.T) {
	schema := loadSchema(t)
	assert.Equal(t, []int{Version}, schema.Properties[keyVersion].Enum)
	assert.Equal(t, "Dockerfile", schema.Defs["Service"].Properties["dockerfile"].Default)
	assert.Equal(t, ".", schema.Defs["Service"].Properties["context"].Default)
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(readSchema(t)))
	require.NoError(t, err)
	schema := loadSchema(t)
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(schema.ID, document))
	compiled, err := compiler.Compile(schema.ID)
	require.NoError(t, err)
	return compiled
}

func validateAgainstSchema(t *testing.T, schema *jsonschema.Schema, file string) error {
	t.Helper()
	data, err := yaml.YAMLToJSON([]byte(file))
	require.NoError(t, err)
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)
	return schema.Validate(instance)
}

func TestSchemaAcceptsWhatParseAndResolveAccept(t *testing.T) {
	schema := compileSchema(t)
	files := map[string]string{
		"minimal":              minimalFile,
		"null removals":        nullOverrideFile,
		"empty services":       "version: 1\npartial: api\nservices: {}\n",
		"overrides and toggle": tddExample,
	}
	for name, file := range files {
		t.Run(name, func(t *testing.T) {
			parsed, err := Parse([]byte(file))
			require.NoError(t, err)
			for _, env := range environmentNames(parsed) {
				_, resolveErr := Resolve(parsed, env)
				require.NoError(t, resolveErr)
			}
			assert.NoError(t, validateAgainstSchema(t, schema, file))
		})
	}
}

func TestSchemaRejectsNullOutsideOverrides(t *testing.T) {
	schema := compileSchema(t)
	files := map[string]string{
		"null base env value": strings.Replace(minimalFile, "    regions:", "    env: { A: null }\n    regions:", 1),
		"null base routing":   strings.Replace(minimalFile, "    regions:", "    routing: null\n    regions:", 1),
		"null services":       "version: 1\npartial: api\nservices: null\n",
	}
	for name, file := range files {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, validateAgainstSchema(t, schema, file))
		})
	}
}

func TestResolveAppliesTheSchemaNullRemovals(t *testing.T) {
	file, err := Parse([]byte(nullOverrideFile))
	require.NoError(t, err)

	prod, err := Resolve(file, "prod")
	require.NoError(t, err)
	api := prod["api"]
	assert.Nil(t, api.Domains)
	assert.Nil(t, api.Routing)
	assert.Equal(t, map[string]string{"A": "1", "C": "3"}, api.Env)
	require.NotNil(t, api.Health)
	assert.Nil(t, api.Health.Interval)
	assert.Equal(t, "/healthz", api.Health.Path)
	assert.Nil(t, api.Regions[testRegion].Autoscaling)
	assert.NotContains(t, api.Regions, "eu-west-1")
}
