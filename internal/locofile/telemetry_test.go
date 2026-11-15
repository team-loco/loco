package locofile

import "testing"

func TestObservabilityEnvironmentOverridesAreIndependent(t *testing.T) {
	file, err := Parse([]byte(`version: 1
partial: apps
services:
  checkout:
    image: example.com/checkout:v1
    observability:
      logs: false
      traces: true
      autoinjectOtelEnvVars: true
    regions:
      us-east-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
    environments:
      overrides:
        observability:
          traces: false
          autoinjectOtelEnvVars: false
`))
	if err != nil {
		t.Fatal(err)
	}
	services, err := Resolve(file, "overrides")
	if err != nil {
		t.Fatal(err)
	}
	settings := services["checkout"].Observability
	if settings.Logs == nil || *settings.Logs || settings.Traces == nil || *settings.Traces ||
		settings.AutoinjectOtelEnvVars == nil || *settings.AutoinjectOtelEnvVars || settings.Metrics != nil {
		t.Fatalf("observability=%+v", settings)
	}
}
