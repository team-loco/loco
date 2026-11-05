package token

import (
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
)

var errNotLoggedIn = errors.New("not logged in - nothing to revoke")

func buildRevokeCmd() *cobra.Command {
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

			store, err := keychain.ForCurrentUser()
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

			confirmed, err := cmdutil.Confirm(
				cmd, "Are you sure you want to revoke your token? You will need to login again.",
			)
			if err != nil || !confirmed {
				return err
			}

			if t.Host != "" {
				host = t.Host
			}
			err = cmdutil.RevokeToken(ctx, host, t.Token)
			if err != nil && connect.CodeOf(err) != connect.CodeUnauthenticated {
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
