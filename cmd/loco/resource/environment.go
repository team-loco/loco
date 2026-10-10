package resource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/internal/ui"
)

const envVarEnvironment = "LOCO_ENV"

var errNoEnvironments = errors.New("the workspace has no environments; create one in the web app first")

func ResolveEnvironmentID(
	ctx context.Context,
	cmd *cobra.Command,
	client environmentv1connect.EnvironmentServiceClient,
	selectFromList func(title string, options []ui.SelectOption) (any, error),
	interactive bool,
	authHeader string,
	workspaceID string,
) (string, error) {
	name, err := cmd.Flags().GetString("env")
	if err != nil {
		return "", fmt.Errorf("error reading env flag: %w", err)
	}
	if name == "" {
		name = os.Getenv(envVarEnvironment)
	}

	req := connect.NewRequest(&environmentv1.ListEnvironmentsRequest{WorkspaceId: workspaceID})
	req.Header().Set("Authorization", authHeader)
	resp, err := client.ListEnvironments(ctx, req)
	if err != nil {
		return "", fmt.Errorf("list environments: %w", err)
	}
	environments := resp.Msg.GetEnvironments()
	if len(environments) == 0 {
		return "", errNoEnvironments
	}

	names := make([]string, 0, len(environments))
	for _, env := range environments {
		envName := env.GetName()
		if name != "" && envName == name {
			return env.GetId(), nil
		}
		names = append(names, envName)
	}
	listed := strings.Join(names, ", ")
	if name != "" {
		return "", fmt.Errorf("environment %q not found; the workspace has %s", name, listed)
	}
	if len(environments) == 1 {
		return environments[0].GetId(), nil
	}
	if !interactive {
		return "", fmt.Errorf(
			"the workspace has several environments (%s); pick one with --env or %s",
			listed,
			envVarEnvironment,
		)
	}

	options := make([]ui.SelectOption, len(environments))
	for i, env := range environments {
		label := env.GetName()
		tier := env.GetType().String()
		id := env.GetId()
		options[i] = ui.SelectOption{Label: label, Description: tier, Value: id}
	}
	selected, err := selectFromList("Select the environment to deploy to", options)
	if err != nil {
		return "", fmt.Errorf("environment selection canceled: %w", err)
	}
	id, ok := selected.(string)
	if !ok {
		return "", fmt.Errorf("invalid environment ID: expected string, got %T", selected)
	}
	return id, nil
}
