package loco

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const testStackName = "storefront"

func testService() Service {
	return Service{
		Key:           "api",
		Build:         &DockerBuild{},
		Routing:       Routing{Port: 8000},
		PrimaryRegion: "us-east-1",
		Regions: map[string]Region{
			"us-east-1": {CPU: "100m", Memory: "256Mi", ReplicasMin: 1, ReplicasMax: 2},
		},
		Health: Health{Path: "/health"},
	}
}

func TestEvaluateSuppliesContext(t *testing.T) {
	var output bytes.Buffer
	input := `{"workspace":"storefront","environment":"production",` +
		`"environmentType":"production","projectRoot":"/project"}`
	err := Evaluate(strings.NewReader(input), &output, func(ctx Context) Stack {
		if ctx.Environment != "production" || ctx.ProjectRoot != "/project" || ctx.Workspace != testStackName {
			t.Fatalf("unexpected context: %+v", ctx)
		}
		service := testService()
		service.Domain = &PlatformDomain{Hostname: "api-" + ctx.Environment + ".onloco.app"}
		return Stack{Name: testStackName, Services: []Service{service}}
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(output.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ProtocolVersion ||
		manifest.Stack.Services[0].Domain.Hostname != "api-production.onloco.app" {
		t.Fatalf("unexpected manifest: %+v", manifest)
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
			if err := Decode(strings.NewReader(input), &ctx); err == nil {
				t.Fatal("accepted invalid context")
			}
		})
	}
}

func TestNormalizePreservesExplicitZeroAndFalse(t *testing.T) {
	service := testService()
	service.Routing.IdleTimeout = Value(int32(0))
	service.Observability.Logging.Enabled = Value(false)
	service.Observability.Tracing.SampleRate = Value(0.0)
	manifest := Manifest{Version: ProtocolVersion, Stack: Stack{Name: testStackName, Services: []Service{service}}}
	if err := Normalize(&manifest); err != nil {
		t.Fatal(err)
	}
	got := manifest.Stack.Services[0]
	if *got.Routing.IdleTimeout != 0 || *got.Observability.Logging.Enabled ||
		*got.Observability.Tracing.SampleRate != 0 {
		t.Fatal("normalization overwrote explicit zero or false")
	}
	if got.Build.Context != "." || got.Build.Dockerfile != "Dockerfile" || got.Health.FailThreshold != 3 {
		t.Fatal("normalization did not fill omitted values")
	}
}

func TestNormalizeRejectsAmbiguousSourcesAndAutoscaling(t *testing.T) {
	for _, change := range []func(*Service){
		func(s *Service) { s.Image = "registry/image:tag" },
		func(s *Service) { s.Build = nil },
		func(s *Service) {
			r := s.Regions[s.PrimaryRegion]
			r.Autoscaling = &Autoscaling{CPUTarget: Value(int32(50)), MemoryTarget: Value(int32(50))}
			s.Regions[s.PrimaryRegion] = r
		},
		func(s *Service) {
			s.Env = map[string]Variable{"DATABASE_URL": {Kind: VariableSecret, Value: "plaintext"}}
		},
	} {
		service := testService()
		change(&service)
		manifest := Manifest{Version: ProtocolVersion, Stack: Stack{Name: "app", Services: []Service{service}}}
		if err := Normalize(&manifest); err == nil {
			t.Fatal("accepted ambiguous service configuration")
		}
	}
}
