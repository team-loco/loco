package locofile

import (
	"encoding/json/v2"
	"fmt"
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
      us-ca-1: {cpu: 100m, memory: 64Mi, replicas: {min: 1, max: 1}}
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

func TestRuntimeUserIDs(t *testing.T) {
	schema := compileSchema(t)
	for _, field := range []string{"runAsUser", "runAsGroup"} {
		for _, id := range []int64{-1, 0, 1654, 65535, 65536} {
			t.Run(fmt.Sprintf("%s/%d", field, id), func(t *testing.T) {
				source := strings.ReplaceAll(runtimeUserFile, field+": 1654", fmt.Sprintf("%s: %d", field, id))
				// Exercise the environment override as well as the service field.
				source = strings.ReplaceAll(source, field+": 82", fmt.Sprintf("%s: %d", field, id))
				_, err := Parse([]byte(source))
				valid := id >= 0 && id <= 65535
				if (err == nil) != valid {
					t.Errorf("parse: %v", err)
				}
				if err := validateAgainstSchema(t, schema, source); (err == nil) != valid {
					t.Errorf("schema: %v", err)
				}
			})
		}
	}
}
