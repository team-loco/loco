package resource

import (
	"testing"

	"github.com/team-loco/loco/internal/config"
)

func TestConfigToResourceSpecMarksMetadataRegionPrimary(t *testing.T) {
	const primary = "us-west-1"
	resources := config.Resources{CPU: "100m", Memory: "256Mi", ReplicasMin: 1, ReplicasMax: 1}
	cfg := &config.LocoConfig{
		Metadata: config.Metadata{Region: primary},
		RegionConfig: map[string]config.Resources{
			"eu-west-1": resources,
			"us-east-1": resources,
			primary:     resources,
		},
	}

	for range 20 {
		spec, err := configToResourceSpec(cfg, "v1")
		if err != nil {
			t.Fatalf("configToResourceSpec: %v", err)
		}
		for name, target := range spec.GetService().GetRegions() {
			want := name == primary
			if target.GetPrimary() != want {
				t.Fatalf("region %s: primary = %v, want %v", name, target.GetPrimary(), want)
			}
		}
	}
}
