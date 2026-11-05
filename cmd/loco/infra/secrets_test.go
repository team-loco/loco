package infra

import (
	"errors"
	"io"
	"testing"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/locofile"
)

const secretService = "mailer"

func TestProvisionForSecretsLeavesOtherPlanErrors(t *testing.T) {
	services := map[string]locofile.Service{secretService: {Secrets: []string{"TOKEN"}}}
	tests := map[string][]*planv1.PlanError{
		"another path":           {{Service: secretService, Path: "port"}},
		"service has no secrets": {{Service: "scheduler", Path: secretsPath}},
		"mixed":                  {{Service: secretService, Path: secretsPath}, {Service: secretService, Path: "port"}},
	}
	for name, planErrors := range tests {
		t.Run(name, func(t *testing.T) {
			plan := &planv1.PlanResponse{Errors: planErrors}
			confirm := confirmations{yes: true}
			err := provisionForSecrets(t.Context(), cmdutil.Target{}, nil, plan, services, confirm, io.Discard)
			if !errors.Is(err, errSecretsNotHandled) {
				t.Fatalf("provisionForSecrets() = %v, want %v", err, errSecretsNotHandled)
			}
		})
	}
}
