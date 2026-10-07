package infra

import (
	"context"
	"testing"

	loco "github.com/team-loco/loco/sdk/go"
	"google.golang.org/protobuf/proto"
)

func TestPullAuthoringCompilesAndPreservesExplicitValues(t *testing.T) {
	definition := definitionFixture(t, "package main\nfunc main() {}\n")
	manifest := &loco.Manifest{
		Version: loco.ProtocolVersion,
		Stack: &loco.Stack{Version: loco.ProtocolVersion, Name: "storefront", Services: []*loco.Service{
			{
				Key:    testServiceKey,
				Source: loco.Image("registry.example.com/api:stable"),
				Spec: loco.Config(&loco.ServiceSpec{
					Routing: &loco.Routing{Port: 8000, IdleTimeout: loco.Value(int32(0))},
					Regions: map[string]*loco.Region{
						"us-east-1": {
							Enabled:     true,
							Primary:     true,
							Cpu:         "100m",
							Memory:      "256Mi",
							MinReplicas: 1,
							MaxReplicas: 1,
						},
					},
					HealthCheck: &loco.Health{Path: "/health"},
					Observability: &loco.Observability{
						Logging: &loco.Logging{Enabled: loco.Value(false)},
						Tracing: &loco.Tracing{SampleRate: loco.Value(float64(0))},
					},
				}),
				Variables: map[string]*loco.Variable{
					"TOKEN":    loco.SecretRef("api-token"),
					"EMPTY":    loco.Literal(""),
					"EXISTING": loco.Preserve(),
				},
			},
		}},
	}

	if err := WriteAuthoring(definition.ProjectRoot, manifest, true); err != nil {
		t.Fatal(err)
	}
	actual, err := (Evaluator{}).Evaluate(context.Background(), definition, &loco.Context{Environment: testProduction})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(actual, manifest) {
		t.Fatalf("authoring roundtrip changed manifest: %+v", actual)
	}
	if err = WriteAuthoring(definition.ProjectRoot, manifest, false); err == nil {
		t.Fatal("pull overwrote authoring without force")
	}
}
