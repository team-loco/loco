package service

import (
	"testing"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

func TestPlanRequestValidation(t *testing.T) {
	envID := uuid.NewString()
	file := []byte("version: 1\n")
	valid := &planv1.PlanRequest{File: file, EnvironmentId: envID}
	if err := protovalidate.Validate(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	invalid := map[string]*planv1.PlanRequest{
		"empty file":          {EnvironmentId: envID},
		"environment not id":  {File: file, EnvironmentId: "prod"},
		"missing environment": {File: file},
	}
	for name, req := range invalid {
		if err := protovalidate.Validate(req); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
