package loco

import (
	"context"
	"fmt"
	"log/slog"
	"os/user"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/loco-team/loco/internal/client"
	"github.com/loco-team/loco/internal/config"
	"github.com/loco-team/loco/internal/keychain"
	"github.com/loco-team/loco/internal/ui"
	"github.com/spf13/cobra"
)

var useCmd = &cobra.Command{
	Use:   "use [org-name]/[workspace-name]",
	Short: "Switch to a different organization and workspace",
	Long:  "Switch your current context to a different organization and workspace.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return useCmdFunc(cmd, args)
	},
}

func useCmdFunc(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	parts := strings.Split(args[0], "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid format - use: loco use <org-name>/<workspace-name>")
	}

	orgName := parts[0]
	workspaceName := parts[1]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	host, err := getHost(cmd)
	if err != nil {
		return err
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	t, err := keychain.GetLocoToken(currentUser.Name)
	if err != nil {
		slog.Error("failed keychain token grab", "error", err)
		return err
	}

	apiClient := client.NewClient(host, t.Token)

	orgs, err := apiClient.GetCurrentUserOrgs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get organizations: %w", err)
	}

	var orgID int64
	for _, org := range orgs {
		if org.Name == orgName {
			orgID = org.Id
			break
		}
	}

	if orgID == 0 {
		return fmt.Errorf("organization '%s' not found", orgName)
	}

	workspaces, err := apiClient.GetUserWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("failed to get workspaces: %w", err)
	}

	var workspaceID int64
	for _, ws := range workspaces {
		if ws.Name == workspaceName && ws.OrgId == orgID {
			workspaceID = ws.Id
			break
		}
	}

	if workspaceID == 0 {
		return fmt.Errorf("workspace '%s' not found in organization '%s'", workspaceName, orgName)
	}

	if err := cfg.SetDefaultScope(
		config.SimpleOrg{ID: orgID, Name: orgName},
		config.SimpleWorkspace{ID: workspaceID, Name: workspaceName},
	); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	successMsg := lipgloss.NewStyle().
		Foreground(ui.LocoLightGreen).
		Render(fmt.Sprintf("✓ Switched to %s/%s", orgName, workspaceName))
	fmt.Println(successMsg)

	return nil
}
