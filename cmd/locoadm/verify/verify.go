package verify

import (
	"context"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/team-loco/loco/cmd/internal/auth"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/installation"
)

const verificationTimeout = 30 * time.Minute

var errTimeout = errors.New("verification timeout must be positive")

func Command() *cobra.Command {
	var timeout time.Duration
	var target installation.VerificationTarget
	command := &cobra.Command{
		Use:   "verify --worker name --environment id",
		Short: "Build disposable source, verify HTTPS and logs, then remove the probe",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if timeout <= 0 {
				return errTimeout
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			status, err := installation.VerifyAPI(ctx, login.Clients, target)
			if err != nil {
				return err
			}
			return json.MarshalWrite(cmd.OutOrStdout(), status)
		},
	}
	command.Flags().String("host", "", "API host URL")
	command.Flags().StringVar(&target.Worker, "worker", "", "Build-enabled worker to verify")
	command.Flags().
		StringVar(&target.EnvironmentID, "environment", "", "Authorized environment for the disposable probe")
	command.Flags().StringVar(
		&target.CAFile, "ca-file", "", "PEM certificates the HTTPS probes trust in addition to the system roots",
	)
	command.Flags().DurationVar(&timeout, "timeout", verificationTimeout, "Maximum duration of source verification")
	for _, flag := range []string{"worker", "environment"} {
		if err := command.MarkFlagRequired(flag); err != nil {
			panic(err)
		}
	}
	return command
}
