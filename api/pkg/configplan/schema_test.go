package configplan

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"k8s.io/apimachinery/pkg/api/resource"

	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	schemaID    = "https://loco.build/schemas/loco.v1.json"
	validCPU    = "250m"
	validMemory = "256Mi"
	mebibyte    = 1024 * 1024
)

func TestSchemaSizeBoundsMatchResourceValidation(t *testing.T) {
	compiler := schemaCompiler(t)
	for _, def := range []string{"Region", "OverrideRegion"} {
		t.Run(def, func(t *testing.T) {
			cpuSchema := compileProperty(t, compiler, def, "cpu")
			for millis := range 2 * locoControllerV1.MaxCPUMillis {
				cores := strconv.FormatFloat(float64(millis)/1000, 'f', -1, 64)
				for _, cpu := range []string{fmt.Sprintf("%dm", millis), cores} {
					spec := resourcesSpec(cpu, validMemory, 1, 1)
					assertSchemaAgrees(t, cpuSchema, cpu, true, spec.Validate())
				}
			}
			memorySchema := compileProperty(t, compiler, def, "memory")
			for mebibytes := range 2 * locoControllerV1.MaxMemoryBytes / mebibyte {
				memory := fmt.Sprintf("%dMi", mebibytes)
				spec := resourcesSpec(validCPU, memory, 1, 1)
				assertSchemaAgrees(t, memorySchema, memory, true, spec.Validate())
				if mebibytes%64 != 0 {
					continue
				}
				memory = strconv.FormatFloat(float64(mebibytes)/1024, 'f', -1, 64) + "Gi"
				spec = resourcesSpec(validCPU, memory, 1, 1)
				// The schema takes sizes below 1Gi only in Mi.
				assertSchemaAgrees(t, memorySchema, memory, mebibytes >= 1024, spec.Validate())
			}
		})
	}
}

func TestSchemaReplicaBoundsMatchResourceValidation(t *testing.T) {
	compiler := schemaCompiler(t)
	for _, def := range []string{"Replicas", "OverrideReplicas"} {
		t.Run(def, func(t *testing.T) {
			lowest := compileProperty(t, compiler, def, "min")
			highest := compileProperty(t, compiler, def, "max")
			for count := range locoControllerV1.MaxReplicas + 2 {
				lowestSpec := resourcesSpec(validCPU, validMemory, int32(count), locoControllerV1.MaxReplicas)
				assertSchemaAgrees(t, lowest, count, true, lowestSpec.Validate())
				highestSpec := resourcesSpec(validCPU, validMemory, locoControllerV1.MinReplicas, int32(count))
				assertSchemaAgrees(t, highest, count, true, highestSpec.Validate())
			}
		})
	}
}

func TestSchemaSizeDescriptionsStateTheBounds(t *testing.T) {
	data := readSchema(t)
	cpu := fmt.Sprintf("from %dm to %dm", locoControllerV1.MinCPUMillis, locoControllerV1.MaxCPUMillis)
	memory := fmt.Sprintf("from %s to %s",
		resource.NewQuantity(locoControllerV1.MinMemoryBytes, resource.BinarySI),
		resource.NewQuantity(locoControllerV1.MaxMemoryBytes, resource.BinarySI))
	for _, bounds := range []string{cpu, memory} {
		if got := strings.Count(string(data), bounds); got != 2 {
			t.Errorf("schema states %q %d times, want once in Region and once in OverrideRegion", bounds, got)
		}
	}
}

// assertSchemaAgrees fails when the schema accepts a value the API rejects, and when exact
// is set, also when the schema rejects a value the API accepts.
func assertSchemaAgrees(t *testing.T, schema *jsonschema.Schema, instance any, exact bool, apiErr error) {
	t.Helper()
	schemaAccepts := schema.Validate(instance) == nil
	apiAccepts := apiErr == nil
	if schemaAccepts && !apiAccepts {
		t.Errorf("schema accepts %v, which the API rejects: %v", instance, apiErr)
	}
	if exact && apiAccepts && !schemaAccepts {
		t.Errorf("schema rejects %v, which the API accepts", instance)
	}
}

func resourcesSpec(cpu, memory string, lowest, highest int32) locoControllerV1.ResourcesSpec {
	return locoControllerV1.ResourcesSpec{
		CPU:      cpu,
		Memory:   memory,
		Replicas: locoControllerV1.ReplicasSpec{Min: lowest, Max: highest},
	}
}

func readSchema(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "loco.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func schemaCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(readSchema(t)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, document); err != nil {
		t.Fatal(err)
	}
	return compiler
}

func compileProperty(t *testing.T, compiler *jsonschema.Compiler, def, property string) *jsonschema.Schema {
	t.Helper()
	schema, err := compiler.Compile(schemaID + "#/$defs/" + def + "/properties/" + property)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
