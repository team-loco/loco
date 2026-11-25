package admin

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/auth"
	"github.com/team-loco/loco/cmd/locoadm/internal/lastactive"
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
			return printRoles(cmd.OutOrStdout(), listed)
		},
	}
	command.Flags().String("host", "", "API host URL")
	return command
}

func printRoles(out io.Writer, listed *adminv1.ListSystemRolesResponse) error {
	holders := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(holders, "USER ID\tNAME\tEMAIL\tCONFIGURED\tGRANTED\tLAST ACTIVE\tISSUER\tSUBJECT")
	for _, holder := range listed.GetHolders() {
		fmt.Fprintf(holders, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", holder.GetUserId(), holder.GetName(),
			holder.GetEmail(), roleName(holder.GetConfiguredRole()), roleName(holder.GetGrantedRole()),
			lastactive.Format(holder.GetLastActiveAt()), holder.GetIssuer(), holder.GetSubject())
	}
	if err := holders.Flush(); err != nil || len(listed.GetUnclaimed()) == 0 {
		return err
	}
	fmt.Fprintln(out)
	unclaimed := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(unclaimed, "UNCLAIMED EMAIL\tROLE")
	for _, entry := range listed.GetUnclaimed() {
		fmt.Fprintf(unclaimed, "%s\t%s\n", entry.GetEmail(), roleName(entry.GetRole()))
	}
	return unclaimed.Flush()
}

// roleName returns the role as the --role flag spells it, or a dash for none.
func roleName(role adminv1.SystemRole) string {
	for name, value := range roles {
		if value == role {
			return name
		}
	}
	return "—"
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
