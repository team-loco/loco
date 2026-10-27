package user

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	userSuspend   = "suspend"
	userReinstate = "reinstate"
)

type dependencies struct {
	token  func(*cobra.Command) (*keychain.UserToken, error)
	client func(string, string) userv1connect.UserServiceClient
}

func BuildUserCmd() *cobra.Command {
	return newUserCmd(dependencies{
		token: cmdutil.GetCurrentLocoToken,
		client: func(host, token string) userv1connect.UserServiceClient {
			return rpc.ForHost(host, token).User
		},
	})
}

func newUserCmd(deps dependencies) *cobra.Command {
	user := &cobra.Command{Use: "user", Short: "Manage users (requires system admin)"}
	statuses := map[string]userv1.UserStatus{
		userSuspend:   userv1.UserStatus_USER_STATUS_SUSPENDED,
		userReinstate: userv1.UserStatus_USER_STATUS_ACTIVE,
	}
	shorts := map[string]string{
		userSuspend:   "Block a user from signing in and using the API",
		userReinstate: "Let a suspended user sign in again",
	}
	for _, operation := range []string{userSuspend, userReinstate} {
		command := &cobra.Command{
			Use:   operation + " <email>",
			Short: shorts[operation],
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				host, err := cmdutil.GetHost(cmd)
				if err != nil {
					return err
				}
				session, err := deps.token(cmd)
				if err != nil {
					return err
				}
				request := &userv1.SetUserStatusRequest{
					Key:    &userv1.SetUserStatusRequest_Email{Email: args[0]},
					Status: statuses[operation],
				}
				if _, err = deps.client(host, session.Token).SetUserStatus(cmd.Context(), request); err != nil {
					return fmt.Errorf("unable to %s %s: %w", operation, args[0], err)
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "User %s: %s\n", args[0], statusWord(statuses[operation]))
				return err
			},
		}
		command.Flags().String("host", "", "API host URL")
		user.AddCommand(command)
	}
	return user
}

func statusWord(status userv1.UserStatus) string {
	if status == userv1.UserStatus_USER_STATUS_SUSPENDED {
		return "suspended"
	}
	return "active"
}
