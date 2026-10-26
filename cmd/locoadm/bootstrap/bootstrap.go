package bootstrap

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	"github.com/team-loco/loco/gen/go/loco/bootstrap/v1/bootstrapv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const tokenFlag = "token"

type dependencies struct {
	token  func(*cobra.Command) (*keychain.UserToken, error)
	client func(string, string) bootstrapv1connect.BootstrapServiceClient
}

func BuildBootstrapCmd() *cobra.Command {
	return newBootstrapCmd(dependencies{
		token: cmdutil.GetCurrentLocoToken,
		client: func(host, token string) bootstrapv1connect.BootstrapServiceClient {
			return rpc.ForHost(host, token).Bootstrap
		},
	})
}

func newBootstrapCmd(deps dependencies) *cobra.Command {
	command := &cobra.Command{
		Use:   "bootstrap --token <token>",
		Short: "Make the signed-in user the installation's first system admin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bootstrapToken, err := cmd.Flags().GetString(tokenFlag)
			if err != nil {
				return err
			}
			host, err := cmdutil.GetHost(cmd)
			if err != nil {
				return err
			}
			session, err := deps.token(cmd)
			if err != nil {
				return err
			}
			client := deps.client(host, session.Token)
			request := &bootstrapv1.BootstrapRequest{Token: bootstrapToken}
			response, err := client.Bootstrap(cmd.Context(), request)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "User %s is now a system admin\n", response.GetUserId())
			return err
		},
	}
	command.Flags().String("host", "", "API host URL")
	command.Flags().String(tokenFlag, "", "The installation's bootstrap token")
	if err := command.MarkFlagRequired(tokenFlag); err != nil {
		panic(err)
	}
	return command
}
