package cmdutil

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

// ResolveEnvironmentID picks the environment named by --env or LOCO_ENV, the workspace's
// only environment, or an interactive choice, and returns its id.
func ResolveEnvironmentID(
	ctx context.Context,
	cmd *cobra.Command,
	client environmentv1connect.EnvironmentServiceClient,
	selectFromList func(title string, options []ui.SelectOption) (any, error),
	interactive bool,
	authHeader string,
	workspaceID string,
) (string, error) {
	env, err := ResolveEnvironment(ctx, cmd, client, selectFromList, interactive, authHeader, workspaceID)
	if err != nil {
		return "", err
	}
	return env.GetId(), nil
}

// ResolveEnvironment picks the environment named by --env or LOCO_ENV, the workspace's
// only environment, or an interactive choice.
func ResolveEnvironment(
	ctx context.Context,
	cmd *cobra.Command,
	client environmentv1connect.EnvironmentServiceClient,
	selectFromList func(title string, options []ui.SelectOption) (any, error),
	interactive bool,
	authHeader string,
	workspaceID string,
) (*environmentv1.Environment, error) {
	name, err := cmd.Flags().GetString("env")
	if err != nil {
		return nil, fmt.Errorf("error reading env flag: %w", err)
	}
	if name == "" {
		name = os.Getenv(envVarEnvironment)
	}

	req := connect.NewRequest(&environmentv1.ListEnvironmentsRequest{WorkspaceId: workspaceID})
	req.Header().Set("Authorization", authHeader)
	resp, err := client.ListEnvironments(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	environments := resp.Msg.GetEnvironments()
	if len(environments) == 0 {
		return nil, errNoEnvironments
	}

	names := make([]string, 0, len(environments))
	for _, env := range environments {
		envName := env.GetName()
		if name != "" && envName == name {
			return env, nil
		}
		names = append(names, envName)
	}
	listed := strings.Join(names, ", ")
	if name != "" {
		return nil, fmt.Errorf("environment %q not found; the workspace has %s", name, listed)
	}
	if len(environments) == 1 {
		return environments[0], nil
	}
	if !interactive {
		return nil, fmt.Errorf(
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
		return nil, fmt.Errorf("environment selection canceled: %w", err)
	}
	id, ok := selected.(string)
	if !ok {
		return nil, fmt.Errorf("invalid environment ID: expected string, got %T", selected)
	}
	for _, env := range environments {
		if env.GetId() == id {
			return env, nil
		}
	}
	return nil, fmt.Errorf("environment %q is not one of the workspace's environments", id)
}
