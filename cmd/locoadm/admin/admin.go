package admin

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/team-loco/loco/cmd/internal/auth"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	"github.com/team-loco/loco/gen/go/loco/admin/v1/adminv1connect"
)

type adminClient = adminv1connect.SystemAdminServiceClient

func BuildAdminCmd() *cobra.Command {
	admin := &cobra.Command{Use: "admin", Short: "Manage system admins (requires system admin)"}
	admin.AddCommand(listCmd(), changeCmd("grant", "Make an existing user a system admin", "granted",
		func(ctx context.Context, client adminClient, userID string) error {
			_, err := client.GrantSystemAdmin(ctx, &adminv1.GrantSystemAdminRequest{UserId: userID})
			return err
		}),
		changeCmd("revoke", "Remove a system admin granted with grant", "revoked",
			func(ctx context.Context, client adminClient, userID string) error {
				_, err := client.RevokeSystemAdmin(ctx, &adminv1.RevokeSystemAdminRequest{UserId: userID})
				return err
			}))
	return admin
}

func listCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List system admins with their identity and source, and configured emails nobody has claimed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			listed, err := login.Clients.SystemAdmin.ListSystemAdmins(cmd.Context(), &adminv1.ListSystemAdminsRequest{})
			if err != nil {
				return fmt.Errorf("unable to list system admins: %w", err)
			}
			encoded, err := protojson.MarshalOptions{Indent: "  "}.Marshal(listed)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return err
		},
	}
	command.Flags().String("host", "", "API host URL")
	return command
}

func changeCmd(
	name, short, result string,
	run func(ctx context.Context, client adminClient, userID string) error,
) *cobra.Command {
	command := &cobra.Command{
		Use:   name + " <user-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("invalid user ID: %w", err)
			}
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			if err = run(cmd.Context(), login.Clients.SystemAdmin, args[0]); err != nil {
				return fmt.Errorf("unable to %s system admin for %s: %w", name, args[0], err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "System admin %s for user %s\n", result, args[0])
			return err
		},
	}
	command.Flags().String("host", "", "API host URL")
	return command
}
