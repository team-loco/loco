package locofile

import "testing"

func TestInjectionEnvironmentOverride(t *testing.T) {
	file, err := Parse([]byte(`version: 1
partial: apps
services:
  checkout:
    image: example.com/checkout:v1
    observability:
      injectOtel: true
    regions:
      us-east-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
    environments:
      overrides:
        observability:
          injectOtel: false
`))
	if err != nil {
		t.Fatal(err)
	}
	services, err := Resolve(file, "overrides")
	if err != nil {
		t.Fatal(err)
	}
	settings := services["checkout"].Observability
	if settings.InjectOtel == nil || *settings.InjectOtel {
		t.Fatalf("observability=%+v", settings)
	}
}
