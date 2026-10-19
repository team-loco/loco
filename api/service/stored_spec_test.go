package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestReconstructResourceSpecKeepsLegacyServiceReadable(t *testing.T) {
	stored := []byte(
		`{
		"observability": {
			"tracing": {"enabled": true, "sampleRate": 0.1},
			"metrics": {"enabled": true, "port": 9090}
		},
		"healthCheck": {"path": "/healthz"}
	}`,
	)
	spec := reconstructResourceSpec(genDb.ResourceTypeService, stored)
	if spec.GetService() == nil {
		t.Fatal("legacy resource lost its service spec")
	}
	if !spec.GetService().GetObservability().GetMetrics().GetEnabled() ||
		spec.GetService().GetHealthCheck().GetPath() != "/healthz" {
		t.Fatalf("legacy resource lost retained settings: %v", spec)
	}
}
