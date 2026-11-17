package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/auth"
	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/internal/rpc"
)

const operationTimeout = 30 * time.Minute

var errTimeout = errors.New("installation timeout must be positive")

func Commands() []*cobra.Command {
	commands := make([]*cobra.Command, 0, 2)
	for _, operation := range []string{"plan", "apply"} {
		var file string
		var timeout time.Duration
		command := &cobra.Command{
			Use:   operation,
			Short: operation + " the declarative installation",
			Args:  cobra.NoArgs,
		}
		command.Flags().StringVar(&file, "file", installation.DefaultFile, installation.FileFlagUsage)
		command.Flags().
			DurationVar(&timeout, "timeout", operationTimeout, "Maximum duration for installation operations")
		command.RunE = func(cmd *cobra.Command, _ []string) error {
			if timeout <= 0 {
				return errTimeout
			}
			config, err := installation.Load(file)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			switch operation {
			case "plan":
				return plan(ctx, cmd, *config, file)
			default:
				return apply(ctx, cmd, *config, file)
			}
		}
		commands = append(commands, command)
	}
	return commands
}

func plan(ctx context.Context, cmd *cobra.Command, config installation.Config, file string) error {
	release, err := installation.LoadRelease(ctx, config, file, cmd.Root().Version, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	planned, err := installation.PlanInstallation(ctx, config, release)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(planned)
}

// apply installs or upgrades the control plane only; workers attach with locoadm cluster attach.
func apply(ctx context.Context, cmd *cobra.Command, config installation.Config, file string) error {
	release, err := installation.LoadRelease(ctx, config, file, cmd.Root().Version, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if err = installation.ApplyControlPlane(ctx, config, release); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if _, err = fmt.Fprintln(out, "Control plane is running. DNS and HTTPS verification remain required."); err != nil {
		return err
	}
	if err = reportAdmins(ctx, out, config); err != nil {
		return err
	}
	flag := ""
	if file != installation.DefaultFile {
		flag = " --file " + file
	}
	_, err = fmt.Fprintf(out, "Next:\n  locoadm login%s\n  locoadm cluster attach%s\n", flag, flag)
	return err
}

// reportAdmins names the configured admins nobody has claimed. Without a system-admin session it
// lists every configured admin, since only a system admin can read the claims.
func reportAdmins(ctx context.Context, out io.Writer, config installation.Config) error {
	token, err := session(ctx, config.ControlPlane.APIURL)
	if err == nil && token != "" {
		admins, adminErr := installation.UnclaimedAdmins(ctx, rpc.ForHost(config.ControlPlane.APIURL, token))
		if adminErr == nil {
			if len(admins.Unclaimed) == 0 {
				_, err = fmt.Fprintln(out, "Every configured admin has signed in.")
				return err
			}
			_, err = fmt.Fprintf(out, "Configured admins who have not signed in: %s\n",
				strings.Join(admins.Unclaimed, ", "))
			return err
		}
	}
	_, err = fmt.Fprintf(
		out,
		"Configured admins: %s. The first verified sign-in with each email makes it a system admin.\n",
		strings.Join(config.ControlPlane.Admins, ", "),
	)
	return err
}

// session returns the saved CLI session for apiURL, or "" when nobody has signed in.
func session(ctx context.Context, apiURL string) (string, error) {
	store, err := keychain.ForCurrentUser()
	if err != nil {
		return "", err
	}
	token, err := auth.FreshToken(ctx, apiURL, store)
	if errors.Is(err, auth.ErrLoginRequired) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return token.Token, nil
}
