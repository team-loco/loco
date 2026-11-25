// Package audit lists the installation-wide audit log.
package audit

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/internal/auth"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func BuildAuditCmd() *cobra.Command {
	var org, query string
	var installationOnly bool
	var limit int32
	command := &cobra.Command{
		Use:   "audit",
		Short: "List recent events of every organization and the installation (requires system read)",
		Example: "  locoadm audit --installation-only\n" +
			"  locoadm audit --org 0192... --query '@type:org.suspended'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if org != "" {
				if _, err := uuid.Parse(org); err != nil {
					return fmt.Errorf("invalid organization ID: %w", err)
				}
			}
			login, err := auth.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			listed, err := login.Clients.Event.ListEvents(cmd.Context(), &eventv1.ListEventsRequest{
				OrgId: org, InstallationOnly: installationOnly, Query: query, PageSize: limit,
			})
			if err != nil {
				return fmt.Errorf("unable to list events: %w", err)
			}
			table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(table, "TIME\tTYPE\tORGANIZATION\tACTOR")
			for _, event := range listed.GetEvents() {
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", event.GetCreatedAt().AsTime().Format(time.RFC3339),
					event.GetType(), orDash(event.GetOrgId()), actor(event))
			}
			return table.Flush()
		},
	}
	command.Flags().String("host", "", "API host URL")
	command.Flags().StringVar(&org, "org", "", "Only the events of this organization ID")
	command.Flags().BoolVar(
		&installationOnly, "installation-only", false, "Only the events that belong to no organization",
	)
	command.Flags().StringVar(&query, "query", "", "Audit query, such as '@type:org.suspended'")
	command.Flags().Int32Var(&limit, "limit", 100, "Number of events to list, at most 500")
	command.MarkFlagsMutuallyExclusive("org", "installation-only")
	return command
}

// actor names who acted; an operator's change in an organization reads "Loco admin".
func actor(event *eventv1.Event) string {
	name := event.GetActorEmail()
	if name == "" {
		name = event.GetActorName()
	}
	if name == "" {
		name = event.GetActorType()
	}
	if event.GetActedAsOperator() {
		return name + " (Loco admin)"
	}
	return name
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
