package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/locofile"
)

const secretsPath = "secrets"

var errSecretsNotSet = errors.New("set the secrets of the new services, then run the command again")

// provisionForSecrets creates the services whose only plan errors are their missing secrets,
// without deploying them, and prints the command that sets each one's secrets. It reports false
// when the plan has any other error, so the caller reports the plan errors instead. The API
// decides which services it may create: it refuses a service the plan does not create.
func provisionForSecrets(
	ctx context.Context,
	t target,
	data []byte,
	plan *planv1.PlanResponse,
	services map[string]locofile.Service,
	confirm confirmations,
	out io.Writer,
) (bool, error) {
	names, ok := servicesMissingOnlySecrets(plan.GetErrors(), services)
	if !ok {
		return false, nil
	}
	if !confirm.yes {
		question := fmt.Sprintf("Create %s without deploying, so its secrets can be set?", strings.Join(names, ", "))
		proceed, err := askToProceed(question)
		if err != nil {
			return true, err
		}
		if !proceed {
			return false, nil
		}
	}
	scope := applyScope{provision: names}
	if _, err := requestApply(ctx, t, data, plan.GetRevision(), plan.GetImages(), confirm, scope); err != nil {
		return true, err
	}
	for _, name := range names {
		assignments := make([]string, 0, len(services[name].Secrets))
		for _, secret := range slices.Sorted(slices.Values(services[name].Secrets)) {
			assignments = append(assignments, secret+"=...")
		}
		fmt.Fprintf(out, "Created %s. Set its secrets with: loco env set --service %s --env %s %s\n",
			name, name, t.environmentName, strings.Join(assignments, " "))
	}
	return true, errSecretsNotSet
}

func servicesMissingOnlySecrets(planErrors []*planv1.PlanError, services map[string]locofile.Service) ([]string, bool) {
	var names []string
	for _, planErr := range planErrors {
		service := planErr.GetService()
		if planErr.GetPath() != secretsPath || len(services[service].Secrets) == 0 {
			return nil, false
		}
		if !slices.Contains(names, service) {
			names = append(names, service)
		}
	}
	slices.Sort(names)
	return names, len(names) > 0
}
