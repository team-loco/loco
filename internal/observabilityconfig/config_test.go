package observabilityconfig

import (
	"testing"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestInjectionDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		resource, deployment *deploymentv1.ObservabilityConfig
		want                 bool
	}{
		{name: "default", want: true},
		{name: "empty", resource: &deploymentv1.ObservabilityConfig{}, want: true},
		{name: "resource disables", resource: &deploymentv1.ObservabilityConfig{InjectOtel: new(false)}},
		{
			name:       "deployment inherits",
			resource:   &deploymentv1.ObservabilityConfig{InjectOtel: new(false)},
			deployment: &deploymentv1.ObservabilityConfig{},
		},
		{name: "deployment disables", deployment: &deploymentv1.ObservabilityConfig{InjectOtel: new(false)}},
		{
			name:       "deployment enables",
			resource:   &deploymentv1.ObservabilityConfig{InjectOtel: new(false)},
			deployment: &deploymentv1.ObservabilityConfig{InjectOtel: new(true)},
			want:       true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.resource, tc.deployment)
			if got.InjectOtel != tc.want || Resolve(nil, got.Proto()) != got {
				t.Fatalf("resolved=%+v want injection=%v", got, tc.want)
			}
		})
	}
}
