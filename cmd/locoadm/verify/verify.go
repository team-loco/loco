package verify

import (
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"
	"time"

	"github.com/team-loco/loco/cmd/internal/auth"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
)

const verificationTimeout = 30 * time.Minute

var errTimeout = errors.New("verification timeout must be positive")

func Command() *cobra.Command {
	var file string
	var timeout time.Duration
	var target installation.VerificationTarget
	command := &cobra.Command{
		Use:   "verify --file installation.yaml --worker name --environment id",
		Short: "Build disposable source, verify HTTPS and logs, then remove the probe",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if timeout <= 0 {
				return errTimeout
			}
			config, err := installation.Load(file)
			if err != nil {
				return err
			}
			manifest := config.Release.Manifest
			if !filepath.IsAbs(manifest) {
				manifest = filepath.Join(filepath.Dir(file), manifest)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			store, err := keychain.ForCurrentUser()
			if err != nil {
				return err
			}
			session, err := auth.FreshToken(ctx, config.ControlPlane.APIURL, store)
			if err != nil {
				return err
			}
			status, err := installation.VerifySource(ctx, *config, manifest, session.Token, target)
			if err != nil {
				return err
			}
			return json.MarshalWrite(cmd.OutOrStdout(), status)
		},
	}
	command.Flags().StringVar(&file, "file", "installation.yaml", "Installation configuration file")
	command.Flags().StringVar(&target.Worker, "worker", "", "Build-enabled worker to verify")
	command.Flags().
		StringVar(&target.EnvironmentID, "environment", "", "Authorized environment for the disposable probe")
	command.Flags().DurationVar(&timeout, "timeout", verificationTimeout, "Maximum duration of source verification")
	for _, flag := range []string{"worker", "environment"} {
		if err := command.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
	return command
}
