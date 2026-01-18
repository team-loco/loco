package token

import (
	"fmt"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/token/internal"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
)

type revokeRunner struct {
	deps *internal.TokenDeps
}

func buildRevokeCmd(deps *internal.TokenDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke current token",
		Long:  "Revoke the current authentication token. You will need to login again.",
		Args:  cobra.NoArgs,
		Example: `  # Revoke current session token (with confirmation)
  loco token revoke

  # Revoke without confirmation
  loco token revoke --force`,
	}

	cmd.Flags().BoolP("force", "f", false, "Skip confirmation prompt")

	if deps != nil {
		runner := &revokeRunner{deps: deps}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *revokeRunner) Run(cmd *cobra.Command) error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	// Check if there's a token to revoke
	_, err = r.deps.GetLocoToken(currentUser.Name)
	if err != nil {
		return fmt.Errorf("not logged in - nothing to revoke")
	}

	force, _ := cmd.Flags().GetBool("force")
	if !force {
		confirm, err := ui.AskYesNo("Are you sure you want to revoke your token? You will need to login again.")
		if err != nil {
			return fmt.Errorf("failed to prompt for confirmation: %w", err)
		}
		if !confirm {
			fmt.Fprintln(r.deps.Stdout, "Aborted.")
			return nil
		}
	}

	// Set an expired token to effectively revoke it
	err = r.deps.SetLocoToken(currentUser.Name, keychain.UserToken{
		Token:     "",
		ExpiresAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		return fmt.Errorf("failed to revoke token: %w", err)
	}

	fmt.Fprintln(r.deps.Stdout, "Token revoked. Please run 'loco login' to authenticate again.")

	return nil
}
