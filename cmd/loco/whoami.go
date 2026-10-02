package loco

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

func newWhoAmICmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "displays information on the logged in user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}

			store, err := env.Tokens()
			if err != nil {
				return err
			}

			t, err := cmdutil.FreshToken(ctx, host, store)
			if err != nil {
				return err
			}

			apiClient := client.NewClient(host, t.Token)
			usr, err := apiClient.GetCurrentUser(ctx)
			if err != nil {
				return fmt.Errorf("failed to get user info: %w", err)
			}

			var currentOrg, currentWorkspace string
			if cfg, cfgErr := session.Load(); cfgErr == nil {
				if scope, scopeErr := cfg.GetScope(); scopeErr == nil {
					currentOrg = scope.Organization.Name
					currentWorkspace = scope.Workspace.Name
				}
			}

			card := renderCardString(usr, currentOrg, currentWorkspace)
			out := cmd.OutOrStdout()
			_, err = lipgloss.Fprintln(out, card)
			return err
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	return cmd
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
