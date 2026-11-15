package observabilityconfig

import (
	"testing"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestDefaultsAndIndependentCollectionAndInjection(t *testing.T) {
	if got := Resolve(nil, nil); got != (Config{Logs: true, Metrics: true, Traces: true}) {
		t.Fatalf("defaults=%+v", got)
	}
	for _, collection := range []bool{true, false} {
		for _, inject := range []bool{true, false} {
			settings := &deploymentv1.ObservabilityConfig{
				Logs: new(
					collection,
				),
				Metrics:               new(collection),
				Traces:                new(collection),
				AutoinjectOtelEnvVars: new(inject),
			}
			want := Config{Logs: collection, Metrics: collection, Traces: collection, AutoinjectOtelEnvVars: inject}
			if got := Resolve(nil, settings); got != want {
				t.Fatalf("resolved=%+v want=%+v", got, want)
			}
			if got := Resolve(nil, want.Proto()); got != want {
				t.Fatalf("snapshot=%+v want=%+v", got, want)
			}
		}
	}
}

func TestDeploymentOverridesOnlyExplicitFields(t *testing.T) {
	resource := &deploymentv1.ObservabilityConfig{
		Logs:                  new(false),
		Metrics:               new(false),
		AutoinjectOtelEnvVars: new(true),
	}
	deployment := &deploymentv1.ObservabilityConfig{Metrics: new(true), Traces: new(false)}
	want := Config{Metrics: true, AutoinjectOtelEnvVars: true}
	if got := Resolve(resource, deployment); got != want {
		t.Fatalf("resolved=%+v want=%+v", got, want)
	}
}
