package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
)

const operationTimeout = 30 * time.Minute

var errTimeout = errors.New("installation timeout must be positive")

func Commands() []*cobra.Command {
	commands := make([]*cobra.Command, 0, 3)
	for _, operation := range []string{"plan", "apply", "status"} {
		var file string
		var timeout time.Duration
		command := &cobra.Command{
			Use:   operation + " --file installation.yaml",
			Short: operation + " the declarative installation",
			Args:  cobra.NoArgs,
		}
		command.Flags().StringVar(&file, "file", "", "Installation YAML file")
		command.Flags().
			DurationVar(&timeout, "timeout", operationTimeout, "Maximum duration for installation operations")
		if err := command.MarkFlagRequired("file"); err != nil {
			panic(err)
		}
		command.RunE = func(cmd *cobra.Command, _ []string) error {
			if timeout <= 0 {
				return errTimeout
			}
			config, err := installation.Load(file)
			if err != nil {
				return err
			}
			manifestPath := config.Release.Manifest
			if !filepath.IsAbs(manifestPath) {
				manifestPath = filepath.Join(filepath.Dir(file), manifestPath)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if operation == "plan" {
				plan, planErr := installation.PlanInstallation(ctx, *config, manifestPath)
				if planErr != nil {
					return planErr
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plan)
			}
			if operation == "status" {
				var token string
				ready, statusErr := installation.BootstrapComplete(ctx, *config)
				if statusErr == nil && ready {
					store, storeErr := keychain.ForCurrentUser()
					if storeErr != nil {
						return storeErr
					}
					session, sessionErr := cmdutil.FreshToken(ctx, config.ControlPlane.APIURL, store)
					if sessionErr != nil {
						return sessionErr
					}
					token = session.Token
				}
				status, inspectErr := installation.StatusInstallation(ctx, *config, token)
				if inspectErr != nil {
					return inspectErr
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
			}
			if err = installation.ApplyControlPlane(ctx, *config, manifestPath); err != nil {
				return err
			}
			ready, err := installation.BootstrapComplete(ctx, *config)
			if err != nil {
				return err
			}
			if !ready {
				_, err = fmt.Fprintln(
					cmd.OutOrStdout(),
					"Control plane is running. Run locoadm bootstrap --file",
					file,
					"--email <first-admin-email> to create the first administrator, then run apply again.",
				)
				return err
			}
			store, err := keychain.ForCurrentUser()
			if err != nil {
				return err
			}
			session, err := cmdutil.FreshToken(ctx, config.ControlPlane.APIURL, store)
			if err != nil {
				return err
			}
			if err = installation.ApplyWorkers(ctx, *config, manifestPath, session.Token); err != nil {
				return err
			}
			_, err = fmt.Fprintln(
				cmd.OutOrStdout(),
				"Control plane and configured workers are running. DNS and HTTPS verification remain required.",
			)
			return err
		}
		commands = append(commands, command)
	}
	return commands
}
