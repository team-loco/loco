package service

import (
	"testing"

	"buf.build/go/protovalidate"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestProtocolAutoscaling(t *testing.T) {
	for name, tc := range map[string]struct {
		scalers *deploymentv1.Scalers
		valid   bool
	}{
		"cpu":         {&deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(70))}, true},
		"memory":      {&deploymentv1.Scalers{Enabled: true, MemoryTarget: new(int32(80))}, true},
		"both":        {&deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(1)), MemoryTarget: new(int32(100))}, true},
		"no target":   {&deploymentv1.Scalers{Enabled: true}, false},
		"zero target": {&deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(0))}, false},
		"negative":    {&deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(-1))}, false},
		"above 100":   {&deploymentv1.Scalers{Enabled: true, MemoryTarget: new(int32(101))}, false},
		"disabled":    {&deploymentv1.Scalers{CpuTarget: new(int32(0))}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := protovalidate.Validate(tc.scalers)
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestProtocolAutoscalingReplicaBounds(t *testing.T) {
	for name, bounds := range map[string][2]int32{"valid": {1, 3}, "zero min": {0, 3}, "max below min": {3, 2}} {
		t.Run(name, func(t *testing.T) {
			service := &deploymentv1.ServiceDeploymentSpec{
				Build: &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage}, Port: 8080,
				MinReplicas: &bounds[0], MaxReplicas: &bounds[1],
				Scalers: &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(70))},
			}
			err := protovalidate.Validate(service)
			if (err == nil) != (name == "valid") {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}
