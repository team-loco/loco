package loco

import (
	"errors"
	"fmt"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
)

func newLogoutCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Log out of loco and revoke the current session token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}

			store, err := env.Tokens()
			if err != nil {
				return err
			}

			t, err := store.Get()
			if errors.Is(err, keychain.ErrNotFound) {
				notLoggedIn := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render("You are not logged in.")
				lipgloss.Fprintln(out, notLoggedIn)
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed to read token from keychain: %w", err)
			}

			if err = cmdutil.RevokeToken(ctx, host, t.Token); err != nil {
				cmdutil.LogRequestID(ctx, err, "failed to revoke token on server")
				if connect.CodeOf(err) != connect.CodeUnauthenticated {
					warning := lipgloss.NewStyle().
						Foreground(ui.LocoOrange).
						Render("Warning: could not revoke the session on the server; the token stays valid until it expires.")
					errOut := cmd.ErrOrStderr()
					lipgloss.Fprintln(errOut, warning)
				}
			}

			if err = store.Delete(); err != nil {
				return fmt.Errorf("failed to delete token from keychain: %w", err)
			}

			checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
			message := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Logged out successfully!")
			lipgloss.Fprintf(out, "%s %s\n", checkmark, message)
			return nil
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	return cmd
}
