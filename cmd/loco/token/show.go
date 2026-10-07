package token

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/internal/keychain"
)

type showDeps struct {
	Tokens func() (keychain.TokenStore, error)
	Output io.Writer
}

func buildShowCmd() *cobra.Command {
	deps := showDeps{
		Tokens: keychain.ForCurrentUser,
		Output: os.Stdout,
	}
	return newShowCmd(deps)
}

func newShowCmd(deps showDeps) *cobra.Command {
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

			store, err := deps.Tokens()
			if err != nil {
				return err
			}

			token, err := store.Get()
			if err != nil {
				return cmdutil.ErrLoginRequired
			}

			raw, err := cmd.Flags().GetBool("raw")
			if err != nil {
				return fmt.Errorf("failed to get raw flag: %w", err)
			}

			if raw {
				fmt.Fprintln(deps.Output, token.Token)
				return nil
			}

			fmt.Fprintf(deps.Output, "User: %s\n", currentUser.Username)

			switch {
			case token.ExpiresAt.IsZero():
				fmt.Fprintln(deps.Output, "Expires: never")
			case token.ExpiresAt.Before(time.Now()):
				fmt.Fprintf(deps.Output, "Expires: %s (EXPIRED)\n", token.ExpiresAt.Format(time.RFC3339))
			default:
				fmt.Fprintf(deps.Output, "Expires: %s\n", token.ExpiresAt.Format(time.RFC3339))
			}

			if len(token.Token) > 8 {
				fmt.Fprintf(deps.Output, "Token: %s...%s\n", token.Token[:4], token.Token[len(token.Token)-4:])
			} else {
				fmt.Fprintln(deps.Output, "Token: ****")
			}
			return nil
		},
	}

	cmd.Flags().Bool("raw", false, "Output raw token value (use with caution)")

	return cmd
}
