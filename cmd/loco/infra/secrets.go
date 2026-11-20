package infra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/internal/locofile"
)

const secretsPath = "secrets"

var (
	errSecretsNotSet     = errors.New("set the secrets of the new services, then run the command again")
	errSecretsNotHandled = errors.New("the plan errors are not only missing secrets")
)

// provisionForSecrets creates the services whose only plan errors are their missing secrets,
// without deploying them, and prints the command that sets each one's secrets. It returns
// errSecretsNotHandled when the plan has any other error or the user declines, and the caller
// reports the plan errors instead. Every other result, including errSecretsNotSet on success, is
// final. The API refuses a service the plan does not create.
func provisionForSecrets(
	ctx context.Context,
	cmd *cobra.Command,
	t cmdutil.Target,
	data []byte,
	plan *planv1.PlanResponse,
	services map[string]locofile.Service,
	confirm confirmations,
) error {
	names, ok := servicesMissingOnlySecrets(plan.GetErrors(), services)
	if !ok {
		return errSecretsNotHandled
	}
	question := fmt.Sprintf("Create %s without deploying, so its secrets can be set?", strings.Join(names, ", "))
	proceed, err := cmdutil.Confirm(cmd, question)
	if err != nil {
		return err
	}
	if !proceed {
		return errSecretsNotHandled
	}
	request := applyRequest{
		target:    t,
		file:      data,
		revision:  plan.GetRevision(),
		images:    plan.GetImages(),
		confirm:   confirm,
		provision: names,
	}
	if _, err = requestApply(ctx, request); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, name := range names {
		assignments := make([]string, 0, len(services[name].Secrets))
		for _, secret := range slices.Sorted(slices.Values(services[name].Secrets)) {
			assignments = append(assignments, secret+"=...")
		}
		fmt.Fprintf(out, "Created %s. Set its secrets with: loco env set --service %s --env %s %s\n",
			name, name, t.Environment.GetSlug(), strings.Join(assignments, " "))
	}
	return errSecretsNotSet
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

// writeSavedSecrets prints each saved secret of the file's services with whether its deployments receive it.
func writeSavedSecrets(w io.Writer, saved []*planv1.SavedSecret) error {
	var b strings.Builder
	service := ""
	for _, secret := range saved {
		if secret.GetService() != service {
			service = secret.GetService()
			fmt.Fprintf(&b, "\nSecrets of %s:\n", service)
		}
		status := "in use"
		if !secret.GetDeclared() {
			status = fmt.Sprintf("Saved. To use this key, add %s to services.%s.secrets and redeploy.",
				secret.GetName(), service)
		}
		fmt.Fprintf(&b, "  %s: %s\n", secret.GetName(), status)
	}
	_, err := fmt.Fprint(w, b.String())
	return err
}
