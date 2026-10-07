package loco

import (
	"bytes"
	"strings"
	"testing"
)

const testStackName = "storefront"

func testService() *Service {
	return &Service{
		Key: "api", Source: Docker(".", "Dockerfile"),
		Spec: Config(&ServiceSpec{
			Routing: &Routing{Port: 8000},
			Regions: map[string]*Region{
				"us-east-1": {
					Enabled:     true,
					Primary:     true,
					Cpu:         "100m",
					Memory:      "256Mi",
					MinReplicas: 1,
					MaxReplicas: 2,
				},
			},
			HealthCheck:   &Health{Path: "/health"},
			Observability: &Observability{Logging: &Logging{}, Tracing: &Tracing{}},
		}),
	}
}

func TestEvaluateSuppliesContext(t *testing.T) {
	var output bytes.Buffer
	input := `{"workspace":"storefront","environment":"production",` +
		`"environmentType":"production","projectRoot":"/project"}`
	err := evaluate(strings.NewReader(input), &output, func(ctx *Context) *Stack {
		if ctx.Environment != "production" || ctx.ProjectRoot != "/project" || ctx.Workspace != testStackName {
			t.Fatalf("unexpected context: %+v", ctx)
		}
		service := testService()
		service.Hostname = "api-" + ctx.Environment + ".onloco.app"
		return &Stack{Name: testStackName, Services: []*Service{service}}
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := decode(bytes.NewReader(output.Bytes()), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ProtocolVersion ||
		manifest.Stack.Services[0].Hostname != "api-production.onloco.app" {
		t.Fatalf("unexpected manifest: %+v", &manifest)
	}
}

func TestDecodeRejectsInvalidDocuments(t *testing.T) {
	for _, input := range []string{
		`{"environment":"production","token":"secret"}`,
		`{"environment":"production","environment":"staging"}`,
		`{"environment":"production"} {}`,
		`{"environment":"production"} log output`,
	} {
		t.Run(input, func(t *testing.T) {
			var ctx Context
			if err := decode(strings.NewReader(input), &ctx); err == nil {
				t.Fatal("accepted invalid context")
			}
		})
	}
}

func TestAuthoringPreservesExplicitZeroAndFalse(t *testing.T) {
	service := testService()
	spec := service.GetSpec().GetService()
	spec.Routing.IdleTimeout = Value(int32(0))
	spec.Observability.Logging.Enabled = Value(false)
	spec.Observability.Tracing.SampleRate = Value(0.0)
	var output bytes.Buffer
	err := evaluate(
		strings.NewReader(`{"environment":"production","projectRoot":"/project"}`),
		&output,
		func(_ *Context) *Stack {
			return &Stack{Name: testStackName, Services: []*Service{service}}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := decode(bytes.NewReader(output.Bytes()), &manifest); err != nil {
		t.Fatal(err)
	}
	got := manifest.GetStack().GetServices()[0].GetSpec().GetService()
	if got.GetRouting().GetIdleTimeout() != 0 || got.GetObservability().GetLogging().GetEnabled() ||
		got.GetObservability().GetTracing().GetSampleRate() != 0 {
		t.Fatal("authoring changed explicit zero or false")
	}
}

func TestAuthoringLeavesApplicationDefaultsUnset(t *testing.T) {
	service := testService()
	var output bytes.Buffer
	err := evaluate(
		strings.NewReader(`{"environment":"production","projectRoot":"/project"}`),
		&output,
		func(_ *Context) *Stack {
			return &Stack{Name: testStackName, Services: []*Service{service}}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := decode(bytes.NewReader(output.Bytes()), &manifest); err != nil {
		t.Fatal(err)
	}
	got := manifest.GetStack().GetServices()[0].GetSpec().GetService()
	if got.GetRouting().GetIdleTimeout() != 0 || got.GetHealthCheck().GetFailureThreshold() != 0 ||
		got.GetObservability().GetLogging().GetRetentionPeriod() != "" {
		t.Fatal("SDK applied application defaults")
	}
}
