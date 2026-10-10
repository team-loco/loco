package infra

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

type target struct {
	host            string
	authHeader      string
	workspaceID     string
	environmentID   string
	environmentName string
}

func addWorkspaceFlags(cmd *cobra.Command) {
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("host", "", "API host URL")
}

func addTargetFlags(cmd *cobra.Command) {
	addWorkspaceFlags(cmd)
	cmd.Flags().String("env", "", "Environment (defaults to the workspace's only environment)")
}

func resolveWorkspaceTarget(ctx context.Context, cmd *cobra.Command) (target, error) {
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return target{}, err
	}
	locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return target{}, err
	}
	apiClient := client.NewClient(host, locoToken.Token)
	workspaceID, err := cmdutil.ResolveWorkspaceID(ctx, cmd, session.Load, apiClient)
	if err != nil {
		return target{}, err
	}
	return target{host: host, authHeader: "Bearer " + locoToken.Token, workspaceID: workspaceID}, nil
}

func resolveTarget(ctx context.Context, cmd *cobra.Command) (target, error) {
	t, err := resolveWorkspaceTarget(ctx, cmd)
	if err != nil {
		return target{}, err
	}
	httpClient := httputil.NewHTTPClient()
	environments := environmentv1connect.NewEnvironmentServiceClient(httpClient, t.host)
	interactive := cmdutil.StdoutIsTerminal()
	env, err := cmdutil.ResolveEnvironment(
		ctx, cmd, environments, ui.SelectFromList, interactive, t.authHeader, t.workspaceID,
	)
	if err != nil {
		return target{}, err
	}
	t.environmentID = env.GetId()
	t.environmentName = env.GetName()
	return t, nil
}

func (t target) planClient() planv1connect.PlanServiceClient {
	httpClient := httputil.NewHTTPClient()
	return planv1connect.NewPlanServiceClient(httpClient, t.host)
}

func (t target) resourceClient() resourcev1connect.ResourceServiceClient {
	httpClient := httputil.NewHTTPClient()
	return resourcev1connect.NewResourceServiceClient(httpClient, t.host)
}

func readLocoFile(path string) ([]byte, error) {
	_, data, err := loadLocoFile(path)
	return data, err
}

func loadLocoFile(path string) (*locofile.File, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	file, parseErr := locofile.Parse(data)
	if parseErr != nil {
		return nil, nil, formatValidationError(path, data, parseErr)
	}
	return file, data, nil
}
