package user

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/team-loco/loco/cmd/internal/auth"

	"github.com/spf13/cobra"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
)

const (
	userSuspend   = "suspend"
	userReinstate = "reinstate"

	listPageSize = 200
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
	user := &cobra.Command{Use: "user", Short: "List and manage users (changing a user requires system admin)"}
	user.AddCommand(buildListCmd())
	for _, op := range operations {
		command := &cobra.Command{
			Use:   op.name + " <email>",
			Short: op.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				login, err := auth.ResolveLogin(cmd)
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

func buildListCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List users with their state and last active time (requires system read)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			query, err := cmd.Flags().GetString("query")
			if err != nil {
				return err
			}
			var users []*userv1.User
			request := &userv1.ListUsersRequest{Query: query, PageSize: listPageSize}
			for {
				listed, listErr := login.Clients.User.ListUsers(cmd.Context(), request)
				if listErr != nil {
					return fmt.Errorf("unable to list users: %w", listErr)
				}
				users = append(users, listed.GetUsers()...)
				if listed.GetNextPageToken() == "" {
					break
				}
				request.PageToken = listed.GetNextPageToken()
			}
			return printUsers(cmd.OutOrStdout(), users)
		},
	}
	command.Flags().String("host", "", "API host URL")
	command.Flags().String("query", "", "Only users whose name or email contains this text")
	return command
}

func printUsers(out io.Writer, users []*userv1.User) error {
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NAME\tEMAIL\tSTATE\tLAST ACTIVE")
	for _, user := range users {
		state := "active"
		if user.GetStatus() == userv1.UserStatus_USER_STATUS_SUSPENDED {
			state = "suspended"
		}
		lastActive := "never"
		if user.GetLastActiveAt() != nil {
			lastActive = user.GetLastActiveAt().AsTime().Format(time.DateTime)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", user.GetName(), user.GetEmail(), state, lastActive)
	}
	return table.Flush()
}
