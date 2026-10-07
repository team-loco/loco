package token

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/ui"
)

var errNotLoggedIn = errors.New("not logged in - nothing to revoke")

type revokeDeps struct {
	Tokens   func() (keychain.TokenStore, error)
	AskYesNo func(prompt string) (bool, error)
}

func buildRevokeCmd() *cobra.Command {
	deps := revokeDeps{
		Tokens:   keychain.ForCurrentUser,
		AskYesNo: ui.AskYesNo,
	}
	return newRevokeCmd(deps)
}

func newRevokeCmd(deps revokeDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke current token",
		Long: "Revoke the current authentication token on the server and remove it from this machine. " +
			"You will need to login again.",
		Args: cobra.NoArgs,
		Example: `  # Revoke current session token (with confirmation)
  loco token revoke

  # Revoke without confirmation
  loco token revoke --yes`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}

			store, err := deps.Tokens()
			if err != nil {
				return err
			}

			t, err := store.Get()
			if errors.Is(err, keychain.ErrNotFound) {
				return errNotLoggedIn
			}
			if err != nil {
				return fmt.Errorf("failed to read token from keychain: %w", err)
			}

			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return fmt.Errorf("failed to get yes flag: %w", err)
			}
			if !yes {
				confirm, confirmErr := deps.AskYesNo(
					"Are you sure you want to revoke your token? You will need to login again.",
				)
				if confirmErr != nil {
					return fmt.Errorf("failed to prompt for confirmation: %w", confirmErr)
				}
				if !confirm {
					fmt.Fprintln(out, "Aborted.")
					return nil
				}
			}

			if t.Host != "" {
				host = t.Host
			}
			err = cmdutil.RevokeToken(ctx, host, t.Token)
			if err != nil && connect.CodeOf(err) != connect.CodeUnauthenticated {
				cmdutil.LogRequestID(ctx, err, "failed to revoke token on server")
				return fmt.Errorf("failed to revoke token on the server: %w", err)
			}

			if err = store.Delete(); err != nil {
				return fmt.Errorf("failed to delete token from keychain: %w", err)
			}

			fmt.Fprintln(out, "Token revoked. Please run 'loco login' to authenticate again.")
			return nil
		},
	}

	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}
