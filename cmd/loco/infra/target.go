package infra

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/locofile"
)

func addWorkspaceFlags(cmd *cobra.Command) {
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("host", "", "API host URL")
}

func addTargetFlags(cmd *cobra.Command) {
	addWorkspaceFlags(cmd)
	cmd.Flags().String("env", "", "Environment (defaults to the workspace's only environment)")
}

func resolveTarget(ctx context.Context, cmd *cobra.Command) (cmdutil.Target, error) {
	t, err := cmdutil.ResolveTarget(ctx, cmd)
	if err != nil {
		return cmdutil.Target{}, err
	}
	interactive := cmdutil.StdoutIsTerminal()
	return t.WithEnvironment(ctx, cmd, interactive)
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
