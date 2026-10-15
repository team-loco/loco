package locofile

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

const runtimeUserFile = `version: 1
partial: worker
services:
  worker:
    image: example.com/worker:v1
    runAsUser: 1654
    runAsGroup: 1654
    regions:
      us-east-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
    environments:
      override:
        runAsUser: 82
        runAsGroup: null
`

func TestRuntimeUserEnvironmentOverrides(t *testing.T) {
	file, err := Parse([]byte(runtimeUserFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"baseline", "override"} {
		services, err := Resolve(file, environment)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(services["worker"])
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if environment == "baseline" {
			if fields["runAsUser"] != float64(1654) || fields["runAsGroup"] != float64(1654) {
				t.Fatalf("baseline IDs = %s", encoded)
			}
		} else if fields["runAsUser"] != float64(82) || fields["runAsGroup"] != nil {
			t.Fatalf("override IDs = %s", encoded)
		}
	}
}

func TestRuntimeUserRejectsRootAndNegativeIDs(t *testing.T) {
	for _, field := range []string{"runAsUser", "runAsGroup"} {
		for _, value := range []string{"0", "-1"} {
			source := strings.Replace(runtimeUserFile, field+": 1654", field+": "+value, 1)
			if _, err := Parse([]byte(source)); err == nil {
				t.Errorf("accepted %s=%s", field, value)
			}
		}
	}
}
