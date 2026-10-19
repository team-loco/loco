package converter

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	genDb "github.com/team-loco/loco/api/gen/db"
)

const legacyServiceSpec = `{
	"observability": {
		"tracing": {"enabled": true, "sampleRate": 0.25, "tags": {"team": "example"}},
		"metrics": {"enabled": true, "path": "/metrics", "port": 9090}
	},
	"healthCheck": {"path": "/healthz"},
	"regions": {"us-east-1": {"enabled": true, "cpu": "100m", "memory": "128Mi"}}
}`

func TestStoredServiceSpecReadsLegacyTracingWithoutLosingSettings(t *testing.T) {
	decoders := map[string]func() error{
		"typed resource": func() error {
			_, err := DeserializeResourceSpec([]byte(legacyServiceSpec), genDb.ResourceTypeService)
			return err
		},
		"resource by type": func() error {
			_, err := DeserializeResourceSpecByType([]byte(legacyServiceSpec), "service")
			return err
		},
	}
	for name, decode := range decoders {
		t.Run(name, func(t *testing.T) {
			if err := decode(); err != nil {
				t.Fatalf("legacy resource: %v", err)
			}
		})
	}
	spec, err := DeserializeStoredServiceSpec([]byte(legacyServiceSpec))
	if err != nil {
		t.Fatal(err)
	}
	if !spec.GetObservability().GetMetrics().GetEnabled() || spec.GetHealthCheck().GetPath() != "/healthz" ||
		spec.GetRegions()[testRegion].GetCpu() != "100m" {
		t.Fatalf("legacy settings lost: %v", spec)
	}
	encoded, err := protojson.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"tracing"`) {
		t.Fatalf("retired tracing setting persisted: %s", encoded)
	}
}

func TestStoredServiceSpecStillRejectsUnknownFields(t *testing.T) {
	for _, data := range []string{
		`{"unknown": true}`,
		`{"observability": {"unknown": true}}`,
		`{"observability": {"tracing": {}, "unknown": true}}`,
		`{"observability": true}`,
	} {
		if _, err := DeserializeStoredServiceSpec([]byte(data)); err == nil {
			t.Errorf("invalid stored spec accepted: %s", data)
		}
	}
}
