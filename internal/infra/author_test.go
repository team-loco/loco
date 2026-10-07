package infra

import (
	"context"
	"reflect"
	"testing"

	loco "github.com/team-loco/loco/sdk/go"
)

func TestPullAuthoringCompilesAndPreservesExplicitValues(t *testing.T) {
	definition := definitionFixture(t, "package main\nfunc main() {}\n")
	manifest := &loco.Manifest{
		Version: loco.ProtocolVersion,
		Stack: loco.Stack{Name: "storefront", Services: []loco.Service{
			{
				Key:           testServiceKey,
				Image:         "registry.example.com/api:stable",
				Routing:       loco.Routing{Port: 8000, IdleTimeout: loco.Value(int32(0))},
				PrimaryRegion: "us-east-1",
				Regions: map[string]loco.Region{
					"us-east-1": {CPU: "100m", Memory: "256Mi", ReplicasMin: 1, ReplicasMax: 1},
				},
				Health: loco.Health{
					Path: "/health",
				},
				Env: map[string]loco.Variable{
					"TOKEN":    loco.SecretRef("api-token"),
					"EMPTY":    loco.Literal(""),
					"EXISTING": loco.Preserve(),
				},
				Observability: loco.Observability{
					Logging: loco.Logging{Enabled: loco.Value(false)},
					Tracing: loco.Tracing{SampleRate: loco.Value(float64(0))},
				},
			},
		}},
	}
	if err := WriteAuthoring(definition.ProjectRoot, manifest, true); err != nil {
		t.Fatal(err)
	}
	actual, err := (Evaluator{}).Evaluate(context.Background(), definition, loco.Context{Environment: testProduction})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, manifest) {
		t.Fatalf("authoring roundtrip changed manifest: %+v", actual)
	}
	if err = WriteAuthoring(definition.ProjectRoot, manifest, false); err == nil {
		t.Fatal("pull overwrote authoring without force")
	}
}
