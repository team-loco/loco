package loco

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	userv1 "github.com/team-loco/loco/proto/loco/user/v1"
	"github.com/team-loco/loco/proto/loco/user/v1/userv1connect"
)

type logoutDeps struct {
	Logout          func(ctx context.Context, host, token string) error
	GetLocoToken    func(username string) (*keychain.UserToken, error)
	DeleteLocoToken func(username string) error
	Output          io.Writer
}

func buildLogoutCmd() *cobra.Command {
	deps := logoutDeps{
		Logout: func(ctx context.Context, host, token string) error {
			httpClient := shared.NewHTTPClient()
			userClient := userv1connect.NewUserServiceClient(httpClient, host)
			req := connect.NewRequest(&userv1.LogoutRequest{})
			req.Header().Set("Authorization", fmt.Sprintf("Bearer %s", token))
			_, err := userClient.Logout(ctx, req)
			return err
		},
		GetLocoToken: func(username string) (*keychain.UserToken, error) {
			return keychain.GetLocoToken(username)
		},
		DeleteLocoToken: func(username string) error {
			return keychain.DeleteLocoToken(username)
		},
		Output: os.Stdout,
	}
	return newLogoutCmd(deps)
}

func newLogoutCmd(deps logoutDeps) *cobra.Command {
	cmd := cobra.Command{
		Use:   "logout",
		Short: "Log out of loco and revoke the current session token",
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
				slog.Debug("no token found in keychain", "error", err)
				fmt.Fprintln(deps.Output, lipgloss.NewStyle().Foreground(ui.LocoLightGray).Render("You are not logged in."))
				return nil
			}

			// Revoke the token on the server
			if err := deps.Logout(ctx, host, t.Token); err != nil {
				slog.Debug("failed to revoke token on server", "error", err)
				// Continue to delete local token even if server revocation fails
			}

			// Delete the token from keychain
			if err := deps.DeleteLocoToken(currentUser.Name); err != nil {
				slog.Debug("failed to delete token from keychain", "error", err)
				return fmt.Errorf("failed to delete token from keychain: %w", err)
			}

			checkmark := lipgloss.NewStyle().Foreground(ui.LocoGreen).Render("✔")
			message := lipgloss.NewStyle().Bold(true).Foreground(ui.LocoOrange).Render("Logged out successfully!")
			fmt.Fprintf(deps.Output, "%s %s\n", checkmark, message)

			return nil
		},
	}
	cmd.Flags().String("host", "", "Set the host URL")
	return &cmd
}
