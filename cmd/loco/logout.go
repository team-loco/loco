package loco

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/httputil"
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

			t, err := env.Tokens.Get()
			if errors.Is(err, keychain.ErrNotFound) {
				notLoggedIn := lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render("You are not logged in.")
				lipgloss.Fprintln(out, notLoggedIn)
				return nil
			}
			if err != nil {
				return fmt.Errorf("failed to read token from keychain: %w", err)
			}

			httpClient := httputil.NewHTTPClient()
			if err = revokeToken(ctx, httpClient, host, t.Token); err != nil {
				cmdutil.LogRequestID(ctx, err, "failed to revoke token on server")
				if connect.CodeOf(err) != connect.CodeUnauthenticated {
					warning := lipgloss.NewStyle().Foreground(ui.LocoOrange).Render("Warning: could not revoke the session on the server; the token stays valid until it expires.")
					errOut := cmd.ErrOrStderr()
					lipgloss.Fprintln(errOut, warning)
				}
			}

			if err = env.Tokens.Delete(); err != nil {
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

func revokeToken(ctx context.Context, httpClient *http.Client, host, token string) error {
	userClient := userv1connect.NewUserServiceClient(httpClient, host)
	req := connect.NewRequest(&userv1.LogoutRequest{})
	req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", token))
	_, err := userClient.Logout(ctx, req)
	return err
}
