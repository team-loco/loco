package admin

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/team-loco/loco/cmd/internal/auth"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
)

var roles = map[string]adminv1.SystemRole{
	"read":  adminv1.SystemRole_SYSTEM_ROLE_READ,
	"admin": adminv1.SystemRole_SYSTEM_ROLE_ADMIN,
}

func BuildAdminCmd() *cobra.Command {
	admin := &cobra.Command{
		Use:   "admin",
		Short: "Manage installation roles: system read and system admin (listing requires system read, changes system admin)",
	}
	admin.AddCommand(listCmd(), grantCmd(), revokeCmd())
	return admin
}

func listCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "list",
		Short: "List users with an installation role, its configured and granted sources, and unclaimed configured emails",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			listed, err := login.Clients.SystemRole.ListSystemRoles(cmd.Context(), &adminv1.ListSystemRolesRequest{})
			if err != nil {
				return fmt.Errorf("unable to list system roles: %w", err)
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

func grantCmd() *cobra.Command {
	var role string
	command := &cobra.Command{
		Use:   "grant <user-id>",
		Short: "Give an existing user an installation role, or change the role an earlier grant gave",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			systemRole, ok := roles[role]
			if !ok {
				return fmt.Errorf("invalid --role %q: use read or admin", role)
			}
			login, err := resolveForUser(cmd, args[0])
			if err != nil {
				return err
			}
			if _, err = login.Clients.SystemRole.GrantSystemRole(cmd.Context(), &adminv1.GrantSystemRoleRequest{
				UserId: args[0], Role: systemRole,
			}); err != nil {
				return fmt.Errorf("unable to grant system %s to %s: %w", role, args[0], err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Granted system %s to user %s\n", role, args[0])
			return err
		},
	}
	command.Flags().String("host", "", "API host URL")
	command.Flags().StringVar(&role, "role", "admin", "Role to grant: read or admin")
	return command
}

func revokeCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "revoke <user-id>",
		Short: "Remove a role granted with grant; a configured role stays",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			login, err := resolveForUser(cmd, args[0])
			if err != nil {
				return err
			}
			if _, err = login.Clients.SystemRole.RevokeSystemRole(cmd.Context(), &adminv1.RevokeSystemRoleRequest{
				UserId: args[0],
			}); err != nil {
				return fmt.Errorf("unable to revoke the system role of %s: %w", args[0], err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Revoked the granted system role of user %s\n", args[0])
			return err
		},
	}
	command.Flags().String("host", "", "API host URL")
	return command
}

func resolveForUser(cmd *cobra.Command, userID string) (auth.Login, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return auth.Login{}, fmt.Errorf("invalid user ID: %w", err)
	}
	return auth.ResolveLogin(cmd)
}
