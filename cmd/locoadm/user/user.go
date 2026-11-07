package user

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

const (
	userSuspend   = "suspend"
	userReinstate = "reinstate"
)

// operation is one user subcommand: the status it sets and the words it prints.
type operation struct {
	name   string
	short  string
	status userv1.UserStatus
	result string
}

var operations = []operation{
	{
		name:   userSuspend,
		short:  "Block a user from signing in and using the API",
		status: userv1.UserStatus_USER_STATUS_SUSPENDED,
		result: "suspended",
	},
	{
		name:   userReinstate,
		short:  "Let a suspended user sign in again",
		status: userv1.UserStatus_USER_STATUS_ACTIVE,
		result: "active",
	},
}

func BuildUserCmd() *cobra.Command {
	user := &cobra.Command{Use: "user", Short: "Manage users (requires system admin)"}
	for _, op := range operations {
		command := &cobra.Command{
			Use:   op.name + " <email>",
			Short: op.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				login, err := cmdutil.ResolveLogin(cmd)
				if err != nil {
					return err
				}
				request := &userv1.SetUserStatusRequest{
					Key:    &userv1.SetUserStatusRequest_Email{Email: args[0]},
					Status: op.status,
				}
				if _, err = login.Clients.User.SetUserStatus(cmd.Context(), request); err != nil {
					return fmt.Errorf("unable to %s %s: %w", op.name, args[0], err)
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "User %s: %s\n", args[0], op.result)
				return err
			},
		}
		command.Flags().String("host", "", "API host URL")
		user.AddCommand(command)
	}
	return user
}
