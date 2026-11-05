package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/session"
)

var (
	errOrgNotSpecified = errors.New(
		"org not specified and no default found. Use --org flag or set LOCO_ORG environment variable",
	)
	errWorkspaceNotSpecified = errors.New(
		"workspace not specified and no default found. Use --workspace flag or set LOCO_WORKSPACE environment variable",
	)
	errNotFound      = errors.New("not found")
	errAmbiguousName = errors.New("pass the ID")
)

// ResolveOrg resolves an organization ID or name from flag > env > config.
// The saved config scope yields the ID.
func ResolveOrg(cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error)) (string, error) {
	org, err := cmd.Flags().GetString("org")
	if err != nil {
		return "", fmt.Errorf("error reading org flag: %w", err)
	}
	if org != "" {
		slog.Debug("using org from flag")
		return org, nil
	}

	org = os.Getenv("LOCO_ORG")
	if org != "" {
		slog.Debug("using org from environment variable")
		return org, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using org from default config")
		return scope.Organization.ID, nil
	}

	return "", errOrgNotSpecified
}

// ResolveWorkspace resolves a workspace ID or name from flag > env > config.
// The saved config scope yields the ID.
func ResolveWorkspace(cmd *cobra.Command, loadConfig func() (*session.SessionConfig, error)) (string, error) {
	workspace, err := cmd.Flags().GetString("workspace")
	if err != nil {
		return "", fmt.Errorf("error reading workspace flag: %w", err)
	}
	if workspace != "" {
		slog.Debug("using workspace from flag")
		return workspace, nil
	}

	workspace = os.Getenv("LOCO_WORKSPACE")
	if workspace != "" {
		slog.Debug("using workspace from environment variable")
		return workspace, nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	scope, err := cfg.GetScope()
	if err == nil {
		slog.Debug("using workspace from default config")
		return scope.Workspace.ID, nil
	}

	return "", errWorkspaceNotSpecified
}

// ResolveWorkspaceID resolves workspace ID, first checking config cache then API.
func ResolveWorkspaceID(
	ctx context.Context,
	cmd *cobra.Command,
	loadConfig func() (*session.SessionConfig, error),
	workspaceClient workspacev1connect.WorkspaceServiceClient,
) (string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}

	workspace, err := ResolveWorkspace(cmd, loadConfig)
	if err != nil {
		return "", err
	}

	org, err := ResolveOrg(cmd, loadConfig)
	if err != nil {
		return "", err
	}

	scope, err := cfg.GetScope()
	if err == nil && org == scope.Organization.ID && workspace == scope.Workspace.ID {
		return scope.Workspace.ID, nil
	}

	workspaces, err := ListUserWorkspaces(ctx, workspaceClient)
	if err != nil {
		return "", fmt.Errorf("failed to get workspaces: %w", err)
	}

	ws, err := FindWorkspace(workspaces, org, workspace)
	if err != nil {
		return "", err
	}
	return ws.GetId(), nil
}

// FindOrg returns the organization whose ID or name is ref. Names are not
// unique, so a name shared by several organizations is an error.
func FindOrg(orgs []*orgv1.Organization, ref string) (*orgv1.Organization, error) {
	return findOne(orgs, ref, "organization", (*orgv1.Organization).GetId, (*orgv1.Organization).GetName)
}

// FindWorkspace returns the workspace whose ID or name is workspace, in the
// organization whose ID or name is org.
func FindWorkspace(workspaces []*workspacev1.Workspace, org, workspace string) (*workspacev1.Workspace, error) {
	var inOrg []*workspacev1.Workspace
	for _, ws := range workspaces {
		if ws.GetOrgId() == org || ws.GetOrgName() == org {
			inOrg = append(inOrg, ws)
		}
	}
	ws, err := findOne(inOrg, workspace, "workspace", (*workspacev1.Workspace).GetId, (*workspacev1.Workspace).GetName)
	if err != nil {
		return nil, fmt.Errorf("in organization %q: %w", org, err)
	}
	return ws, nil
}

func findOne[T any](items []T, ref, kind string, id, name func(T) string) (T, error) {
	var named []T
	for _, item := range items {
		if id(item) == ref {
			return item, nil
		}
		if name(item) == ref {
			named = append(named, item)
		}
	}
	var zero T
	switch len(named) {
	case 0:
		return zero, fmt.Errorf("%s %q %w", kind, ref, errNotFound)
	case 1:
		return named[0], nil
	default:
		return zero, fmt.Errorf("%s name %q matches %d %ss, %w", kind, ref, len(named), kind, errAmbiguousName)
	}
}
