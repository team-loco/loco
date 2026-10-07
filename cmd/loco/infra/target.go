package infra

import (
	"context"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/infra/v1/infrav1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	loco "github.com/team-loco/loco/sdk/go"
)

type Target struct {
	Host          string
	Token         string
	WorkspaceID   string
	EnvironmentID string
	AuthorContext loco.Context
	Client        infrav1connect.InfrastructureServiceClient
}

func targetFlags(cmd *cobra.Command) {
	cmd.Flags().String("host", "", "Loco API host")
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name or ID")
	cmd.Flags().String("environment", "", "Environment name or ID")
	cmd.Flags().String("stack", "", "Stack name")
}

func definitionFlags(cmd *cobra.Command) {
	cmd.Flags().String("file", "", "Go infrastructure entrypoint")
	cmd.Flags().String("project-root", "", "Application project root")
}

func flagOrEnv(cmd *cobra.Command, flag, env string) (string, error) {
	value, err := cmd.Flags().GetString(flag)
	if err != nil {
		return "", err
	}
	if value == "" {
		value = os.Getenv(env)
	}
	return strings.TrimSpace(value), nil
}

func ResolveTarget(cmd *cobra.Command) (*Target, error) {
	if err := linkedDefaults(cmd); err != nil {
		return nil, err
	}
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return nil, err
	}
	token, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return nil, err
	}
	workspace, err := flagOrEnv(cmd, "workspace", "LOCO_WORKSPACE")
	if err != nil {
		return nil, err
	}
	environment, err := flagOrEnv(cmd, "environment", "LOCO_ENVIRONMENT")
	if err != nil {
		return nil, err
	}
	cfg, err := session.Load()
	if err != nil {
		return nil, err
	}
	var workspaceID, workspaceName string
	if looksLikeID(workspace) {
		workspaceID, workspaceName = workspace, workspace
	} else {
		org, orgErr := flagOrEnv(cmd, "org", "LOCO_ORG")
		if orgErr != nil {
			return nil, orgErr
		}
		scope, scopeErr := cfg.GetScope()
		if scopeErr == nil && (workspace == "" || workspace == scope.Workspace.Name) &&
			(org == "" || org == scope.Organization.Name) {
			workspaceID, workspaceName = scope.Workspace.ID, scope.Workspace.Name
		} else {
			workspaceID, workspaceName, err = lookupWorkspace(cmd.Context(), cmd, host, token.Token, workspace)
			if err != nil {
				return nil, err
			}
		}
	}
	if workspaceID == "" || environment == "" {
		return nil, fmt.Errorf("workspace and environment are required; use --workspace and --environment")
	}
	envClient := environmentv1connect.NewEnvironmentServiceClient(httputil.NewHTTPClient(), host)
	var env *environmentv1.Environment
	if looksLikeID(environment) {
		request := connect.NewRequest(&environmentv1.GetEnvironmentRequest{EnvironmentId: environment})
		request.Header().Set("Authorization", "Bearer "+token.Token)
		response, getEnvironmentErr := envClient.GetEnvironment(cmd.Context(), request)
		if getEnvironmentErr != nil {
			return nil, fmt.Errorf("resolve environment: %w", getEnvironmentErr)
		}
		env = response.Msg.GetEnvironment()
	} else {
		request := connect.NewRequest(&environmentv1.ListEnvironmentsRequest{WorkspaceId: workspaceID})
		request.Header().Set("Authorization", "Bearer "+token.Token)
		response, listEnvironmentsErr := envClient.ListEnvironments(cmd.Context(), request)
		if listEnvironmentsErr != nil {
			return nil, fmt.Errorf("list environments: %w", listEnvironmentsErr)
		}
		for _, candidate := range response.Msg.GetEnvironments() {
			if candidate.GetName() == environment {
				env = candidate
				break
			}
		}
	}
	if env == nil || env.GetWorkspaceId() != workspaceID {
		return nil, fmt.Errorf("environment %q does not exist in the selected workspace", environment)
	}
	if env.GetWorkspaceName() != "" {
		workspaceName = env.GetWorkspaceName()
	}
	tier, err := environmentType(env.GetType())
	if err != nil {
		return nil, err
	}
	return &Target{
		Host: host, Token: token.Token, WorkspaceID: workspaceID, EnvironmentID: env.GetId(),
		AuthorContext: loco.Context{Workspace: workspaceName, Environment: env.GetName(), EnvironmentType: tier},
		Client:        infrav1connect.NewInfrastructureServiceClient(httputil.NewHTTPClient(), host),
	}, nil
}

func looksLikeID(value string) bool {
	return len(value) == 36 && value[8] == '-' && value[13] == '-' && value[18] == '-' && value[23] == '-'
}

func environmentType(value environmentv1.EnvironmentType) (string, error) {
	switch value {
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_DEV:
		return "dev", nil
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING:
		return "staging", nil
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION:
		return "production", nil
	default:
		return "", fmt.Errorf("unsupported environment type")
	}
}

func lookupWorkspace(ctx context.Context, cmd *cobra.Command, host, token, name string) (string, string, error) {
	if name == "" {
		return "", "", fmt.Errorf("workspace is required")
	}
	org, err := flagOrEnv(cmd, "org", "LOCO_ORG")
	if err != nil {
		return "", "", err
	}
	api := client.NewClient(host, token)
	user, err := api.GetCurrentUser(ctx)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace; CI tokens should supply workspace and environment IDs: %w", err)
	}
	workspaces, err := api.GetUserWorkspaces(ctx, user.GetId())
	if err != nil {
		return "", "", err
	}
	var orgID string
	if org != "" {
		orgs, getCurrentUserOrgsErr := api.GetCurrentUserOrgs(ctx, user.GetId())
		if getCurrentUserOrgsErr != nil {
			return "", "", getCurrentUserOrgsErr
		}
		for _, candidate := range orgs {
			if candidate.GetName() == org {
				orgID = candidate.GetId()
			}
		}
		if orgID == "" {
			return "", "", fmt.Errorf("organization %q was not found", org)
		}
	}
	var id string
	for _, candidate := range workspaces {
		if candidate.GetName() == name && (orgID == "" || orgID == candidate.GetOrgId()) {
			if id != "" {
				return "", "", fmt.Errorf("workspace name is ambiguous; supply --org or a workspace ID")
			}
			id = candidate.GetId()
		}
	}
	if id == "" {
		return "", "", fmt.Errorf("workspace %q was not found", name)
	}
	return id, name, nil
}
