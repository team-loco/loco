package locofile

import (
	"encoding/json/v2"
	"testing"
)

func TestRollbackOnFailureRoundTrip(t *testing.T) {
	file, err := Parse([]byte(`version: 1
partial: apps
services:
  checkout:
    image: example.com/checkout:v1
    rollbackOnFailure: true
    regions:
      us-east-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
    environments:
      production:
        rollbackOnFailure: false
`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	var decoded File
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	const productionEnvironment = "production"
	for _, env := range []string{"preview", productionEnvironment} {
		services, err := Resolve(&decoded, env)
		if err != nil {
			t.Fatal(err)
		}
		got := services["checkout"].RollbackOnFailure
		if got == nil || *got != (env != productionEnvironment) {
			t.Fatalf("%s rollback=%v", env, got)
		}
	}
}
