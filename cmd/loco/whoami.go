package loco

import (
	"context"
	"fmt"
	"log/slog"
	"os/user"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
	userv1 "github.com/team-loco/loco/shared/proto/user/v1"
)

func init() {
	whoamiCmd.Flags().String("host", "", "Set the host URL")
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "displays information on the logged in user",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

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
			return ErrLoginRequired
		}

		apiClient := client.NewClient(host, t.Token)
		usr, err := apiClient.GetCurrentUser(ctx)
		if err != nil {
			return fmt.Errorf("failed to get user info: %w", err)
		}

		cfg, err := config.Load()
		var currentOrg, currentWorkspace string
		if err == nil {
			scope, err := cfg.GetScope()
			if err == nil {
				currentOrg = scope.Organization.Name
				currentWorkspace = scope.Workspace.Name
			}
		}

		renderCard(usr, currentOrg, currentWorkspace)
		return nil
	},
}

func renderCard(usr *userv1.User, currentOrg, currentWorkspace string) {
	borderColor := ui.LocoOrange
	labelColor := lipgloss.Color("#888888")
	valueColor := lipgloss.Color("#FFFFFF")

	greeting := lipgloss.NewStyle().
		Bold(true).
		Foreground(borderColor).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("👋  Hi, %s!", usr.GetName()))

	labelStyle := lipgloss.NewStyle().
		Foreground(labelColor).
		Bold(true)

	valueStyle := lipgloss.NewStyle().
		Foreground(valueColor)

	row := func(label, value string) string {
		return lipgloss.JoinHorizontal(
			lipgloss.Top,
			labelStyle.Render(label+": "),
			valueStyle.Render(value),
		)
	}

	rows := []string{
		row("Email", usr.GetEmail()),
		row("External ID", usr.GetExternalId()),
		row("Context", fmt.Sprintf("%s/%s", currentOrg, currentWorkspace)),
	}

	body := lipgloss.JoinVertical(lipgloss.Left, rows...)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		greeting,
		"",
		body,
	)

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 5).
		MaxWidth(200).
		Align(lipgloss.Left)

	fmt.Println(cardStyle.Render(content))
}
