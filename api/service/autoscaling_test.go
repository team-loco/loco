package service

import (
	"testing"

	"buf.build/go/protovalidate"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestProtocolRejectsEnabledAutoscaling(t *testing.T) {
	scalers := &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(70))}
	if err := protovalidate.Validate(scalers); err == nil {
		t.Fatal("enabled autoscaling passed protocol validation")
	}
	scalers.Enabled = false
	if err := protovalidate.Validate(scalers); err != nil {
		t.Fatalf("disabled autoscaling rejected: %v", err)
	}
}
