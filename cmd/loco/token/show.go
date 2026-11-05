package token

import (
	"errors"
	"fmt"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
)

func buildShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show current token info",
		Long:  "Display information about the current authentication token.",
		Args:  cobra.NoArgs,
		Example: `  # Show current token info (masked)
  loco token show

  # Show raw token value (for scripting)
  loco token show --raw`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			currentUser, err := user.Current()
			if err != nil {
				return fmt.Errorf("failed to get current user: %w", err)
			}

			store, err := keychain.ForCurrentUser()
			if err != nil {
				return err
			}

			token, err := store.Get()
			if errors.Is(err, keychain.ErrNotFound) {
				return cmdutil.ErrLoginRequired
			}
			if err != nil {
				return fmt.Errorf("failed to read token from keychain: %w", err)
			}

			raw, err := cmd.Flags().GetBool("raw")
			if err != nil {
				return fmt.Errorf("failed to get raw flag: %w", err)
			}

			if raw {
				fmt.Fprintln(cmd.OutOrStdout(), token.Token)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "User: %s\n", currentUser.Username)

			switch {
			case token.ExpiresAt.IsZero():
				fmt.Fprintln(cmd.OutOrStdout(), "Expires: never")
			case token.ExpiresAt.Before(time.Now()):
				fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s (EXPIRED)\n", token.ExpiresAt.Format(time.RFC3339))
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s\n", token.ExpiresAt.Format(time.RFC3339))
			}

			if len(token.Token) > 8 {
				fmt.Fprintf(cmd.OutOrStdout(), "Token: %s...%s\n", token.Token[:4], token.Token[len(token.Token)-4:])
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Token: ****")
			}
			return nil
		},
	}

	cmd.Flags().Bool("raw", false, "Output raw token value (use with caution)")

	return cmd
}
