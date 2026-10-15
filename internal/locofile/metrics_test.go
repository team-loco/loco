package locofile

import (
	"strings"
	"testing"
)

func TestMetricsEndpointAndEnvironmentRemoval(t *testing.T) {
	source := `version: 1
partial: api
services:
  api:
    image: example.com/api:1
    metrics: {port: 9090, path: /metrics}
    regions:
      us-east-1: {cpu: 100m, memory: 256Mi, replicas: {min: 1, max: 1}}
    environments:
      staging: {metrics: null}
`
	file, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := Resolve(file, "production")
	if err != nil {
		t.Fatal(err)
	}
	if resolved["api"].Metrics.Port != 9090 {
		t.Fatal("lost metrics port")
	}
	staging, err := Resolve(file, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if staging["api"].Metrics != nil {
		t.Fatal("metrics endpoint not removed")
	}
	endpoints := []string{
		"{port: 0}", "{port: 65536}", "{port: 9090, path: relative}", "{port: 9090, path: '//host/metrics'}",
	}
	for _, endpoint := range endpoints {
		if _, err := Parse([]byte(strings.Replace(source, "{port: 9090, path: /metrics}", endpoint, 1))); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}
