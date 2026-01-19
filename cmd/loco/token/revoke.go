package token

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
)

type revokeDeps struct {
	GetLocoToken func(username string) (*keychain.UserToken, error)
	SetLocoToken func(username string, token keychain.UserToken) error
	AskYesNo     func(prompt string) (bool, error)
	Output       io.Writer
}

func buildRevokeCmd() *cobra.Command {
	deps := revokeDeps{
		GetLocoToken: keychain.GetLocoToken,
		SetLocoToken: keychain.SetLocoToken,
		AskYesNo:     ui.AskYesNo,
		Output:       os.Stdout,
	}
	return newRevokeCmd(deps)
}

func newRevokeCmd(deps revokeDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke current token",
		Long:  "Revoke the current authentication token. You will need to login again.",
		Args:  cobra.NoArgs,
		Example: `  # Revoke current session token (with confirmation)
  loco token revoke

  # Revoke without confirmation
  loco token revoke --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			currentUser, err := user.Current()
			if err != nil {
				return fmt.Errorf("failed to get current user: %w", err)
			}

			_, err = deps.GetLocoToken(currentUser.Name)
			if err != nil {
				return fmt.Errorf("not logged in - nothing to revoke")
			}

			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				confirm, err := deps.AskYesNo("Are you sure you want to revoke your token? You will need to login again.")
				if err != nil {
					return fmt.Errorf("failed to prompt for confirmation: %w", err)
				}
				if !confirm {
					fmt.Fprintln(deps.Output, "Aborted.")
					return nil
				}
			}

			err = deps.SetLocoToken(currentUser.Name, keychain.UserToken{
				Token:     "",
				ExpiresAt: time.Now().Add(-time.Hour),
			})
			if err != nil {
				return fmt.Errorf("failed to revoke token: %w", err)
			}

			fmt.Fprintln(deps.Output, "Token revoked. Please run 'loco login' to authenticate again.")
			return nil
		},
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}
