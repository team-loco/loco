package loco

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
	userv1 "github.com/team-loco/loco/shared/proto/loco/user/v1"
)

type whoamiDeps struct {
	GetCurrentUser func(ctx context.Context, host, token string) (*userv1.User, error) // Pass context
	GetLocoToken   func(username string) (*keychain.UserToken, error)
	LoadConfig     func() (*config.SessionConfig, error)
	Output         io.Writer
}

func buildWhoAmICmd() *cobra.Command {
	deps := whoamiDeps{
		GetCurrentUser: func(ctx context.Context, host, token string) (*userv1.User, error) {
			apiClient := client.NewClient(host, token)
			return apiClient.GetCurrentUser(ctx)
		},
		GetLocoToken: func(username string) (*keychain.UserToken, error) {
			return keychain.GetLocoToken(username)
		},
		LoadConfig: func() (*config.SessionConfig, error) {
			return config.Load()
		},
		Output: os.Stdout,
	}
	return newWhoAmICmd(deps)
}

func newWhoAmICmd(deps whoamiDeps) *cobra.Command {
	cmd := cobra.Command{
		Use:   "whoami",
		Short: "displays information on the logged in user",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			host, err := getHost(cmd)
			if err != nil {
				return err
			}

			currentUser, err := user.Current()
			if err != nil {
				return fmt.Errorf("failed to get current user: %w", err)
			}

			t, err := deps.GetLocoToken(currentUser.Name)
			if err != nil {
				slog.Error("failed keychain token grab", "error", err)
				return ErrLoginRequired
			}

			usr, err := deps.GetCurrentUser(ctx, host, t.Token)
			if err != nil {
				return fmt.Errorf("failed to get user info: %w", err)
			}

			cfg, err := deps.LoadConfig()
			var currentOrg, currentWorkspace string
			if err == nil {
				scope, err := cfg.GetScope()
				if err == nil {
					currentOrg = scope.Organization.Name
					currentWorkspace = scope.Workspace.Name
				}
			}

			content := renderCardString(usr, currentOrg, currentWorkspace)
			_, err = fmt.Fprintln(deps.Output, content)
			return err
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	return &cmd
}

func renderCardString(usr *userv1.User, currentOrg, currentWorkspace string) string {
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

	return cardStyle.Render(content)
}
