package org

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/auth"
	"github.com/team-loco/loco/cmd/internal/paginate"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const (
	tablePadding = 2
	timeLayout   = "2006-01-02 15:04"
)

var orgStates = map[string]orgv1.OrgState{
	"active":    orgv1.OrgState_ORG_STATE_ACTIVE,
	"suspended": orgv1.OrgState_ORG_STATE_SUSPENDED,
}

func listCmd() *cobra.Command {
	var query, state string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every organization in the installation (requires system read)",
		Example: "  locoadm org list --query acme\n" +
			"  locoadm org list --state suspended",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request := &orgv1.ListOrgsRequest{Query: query}
			if state != "" {
				var ok bool
				if request.State, ok = orgStates[state]; !ok {
					return fmt.Errorf("invalid --state %q: use active or suspended", state)
				}
			}
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			orgs, err := paginate.All(func(token string) ([]*orgv1.OrgSummary, string, error) {
				request.PageToken = token
				response, listErr := login.Clients.Org.ListOrgs(cmd.Context(), request)
				if listErr != nil {
					return nil, "", listErr
				}
				return response.GetOrgs(), response.GetNextPageToken(), nil
			})
			if err != nil {
				return fmt.Errorf("unable to list organizations: %w", err)
			}
			table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, tablePadding, ' ', 0)
			fmt.Fprintln(table, "ID\tNAME\tSTATE\tOWNERS\tMEMBERS\tWORKSPACES\tRESOURCES\tCREATED")
			for _, summary := range orgs {
				org := summary.GetOrganization()
				status := "active"
				if org.GetSuspendedAt() != nil {
					status = "suspended: " + org.GetSuspensionReason()
				}
				owners := make([]string, 0, len(summary.GetOwners()))
				for _, owner := range summary.GetOwners() {
					owners = append(owners, owner.GetEmail())
				}
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
					org.GetId(), org.GetName(), status, strings.Join(owners, ","), org.GetMemberCount(),
					org.GetWorkspaceCount(), summary.GetResourceCount(),
					org.GetCreatedAt().AsTime().Local().Format(timeLayout))
			}
			return table.Flush()
		},
	}
	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().StringVar(&query, "query", "", "Only organizations whose name, ID or owner email contains this text")
	cmd.Flags().StringVar(&state, "state", "", "Only active or suspended organizations")
	return cmd
}
