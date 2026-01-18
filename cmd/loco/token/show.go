package token

import (
	"fmt"
	"os/user"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/token/internal"
)

type showRunner struct {
	deps *internal.TokenDeps
}

func buildShowCmd(deps *internal.TokenDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show current token info",
		Long:  "Display information about the current authentication token.",
		Args:  cobra.NoArgs,
		Example: `  # Show current token info (masked)
  loco token show

  # Show raw token value (for scripting)
  loco token show --raw`,
	}

	cmd.Flags().Bool("raw", false, "Output raw token value (use with caution)")

	if deps != nil {
		runner := &showRunner{deps: deps}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return runner.Run(cmd)
		}
	}

	return cmd
}

func (r *showRunner) Run(cmd *cobra.Command) error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	token, err := r.deps.GetLocoToken(currentUser.Name)
	if err != nil {
		return fmt.Errorf("not logged in - please run 'loco login'")
	}

	raw, _ := cmd.Flags().GetBool("raw")

	if raw {
		fmt.Fprintln(r.deps.Stdout, token.Token)
		return nil
	}

	fmt.Fprintf(r.deps.Stdout, "User: %s\n", currentUser.Name)

	if token.ExpiresAt.IsZero() {
		fmt.Fprintln(r.deps.Stdout, "Expires: never")
	} else if token.ExpiresAt.Before(time.Now()) {
		fmt.Fprintf(r.deps.Stdout, "Expires: %s (EXPIRED)\n", token.ExpiresAt.Format(time.RFC3339))
	} else {
		fmt.Fprintf(r.deps.Stdout, "Expires: %s\n", token.ExpiresAt.Format(time.RFC3339))
	}

	// Show masked token
	if len(token.Token) > 8 {
		fmt.Fprintf(r.deps.Stdout, "Token: %s...%s\n", token.Token[:4], token.Token[len(token.Token)-4:])
	} else {
		fmt.Fprintln(r.deps.Stdout, "Token: ****")
	}

	return nil
}
