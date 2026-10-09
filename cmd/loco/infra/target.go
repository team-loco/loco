package infra

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

type target struct {
	host          string
	authHeader    string
	workspaceID   string
	environmentID string
}

func addTargetFlags(cmd *cobra.Command) {
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment (defaults to the workspace's only environment)")
	cmd.Flags().String("host", "", "API host URL")
}

func resolveTarget(ctx context.Context, cmd *cobra.Command) (target, error) {
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return target{}, err
	}
	locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return target{}, err
	}
	authHeader := "Bearer " + locoToken.Token

	apiClient := client.NewClient(host, locoToken.Token)
	workspaceID, err := cmdutil.ResolveWorkspaceID(ctx, cmd, session.Load, apiClient)
	if err != nil {
		return target{}, err
	}

	httpClient := httputil.NewHTTPClient()
	environments := environmentv1connect.NewEnvironmentServiceClient(httpClient, host)
	interactive := cmdutil.StdoutIsTerminal()
	environmentID, err := cmdutil.ResolveEnvironmentID(
		ctx, cmd, environments, ui.SelectFromList, interactive, authHeader, workspaceID,
	)
	if err != nil {
		return target{}, err
	}
	return target{
		host:          host,
		authHeader:    authHeader,
		workspaceID:   workspaceID,
		environmentID: environmentID,
	}, nil
}

func (t target) planClient() planv1connect.PlanServiceClient {
	httpClient := httputil.NewHTTPClient()
	return planv1connect.NewPlanServiceClient(httpClient, t.host)
}

func readLocoFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if _, parseErr := locofile.Parse(data); parseErr != nil {
		return nil, formatValidationError(path, data, parseErr)
	}
	return data, nil
}
