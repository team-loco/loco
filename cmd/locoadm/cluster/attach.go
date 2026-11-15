package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/auth"
	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
)

const attachTimeout = 30 * time.Minute

// attachCmd attaches the workers of an installation file with a system-admin session.
func attachCmd() *cobra.Command {
	var file string
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "attach [worker-name]",
		Short: "Register, enroll and install the workers of the installation file, or only the named one",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return errAttachTimeout
			}
			config, err := installation.Load(file)
			if err != nil {
				return err
			}
			only := ""
			if len(args) == 1 {
				only = args[0]
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			store, err := keychain.ForCurrentUser()
			if err != nil {
				return err
			}
			session, err := auth.FreshToken(ctx, config.ControlPlane.APIURL, store)
			if err != nil {
				return fmt.Errorf("%w; sign in with locoadm login", err)
			}
			release, err := installation.LoadRelease(ctx, *config, file, cmd.Root().Version, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if err = installation.ApplyWorkers(ctx, *config, release, session.Token, only); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Workers are running. DNS and HTTPS verification remain required.")
			return err
		},
	}
	command.Flags().StringVar(&file, "file", installation.DefaultFile, installation.FileFlagUsage)
	command.Flags().DurationVar(&timeout, "timeout", attachTimeout, "Maximum duration for attaching workers")
	return command
}
