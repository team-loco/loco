package workspace

import (
	"cmp"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

var errNoUpdateFlags = errors.New(
	"at least one update flag is required (--name, --description or a --*-retention-days flag)",
)

func buildUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <name-or-id>",
		Short: "Update a workspace",
		Long: `Update a workspace's name, description or telemetry retention.

Each --*-retention-days flag sets how many days the workspace keeps that signal, from 1 to the
organization's limit; 0 returns the signal to the limit. A change applies to telemetry ingested
after it.`,
		Args: cobra.ExactArgs(1),
		Example: `  # Rename a workspace
  loco workspace update my-workspace --name new-name

  # Update workspace description
  loco workspace update my-workspace --description "Updated description"

  # Update both name and description
  loco workspace update my-workspace --name new-name --description "New description"

  # Keep logs for 7 days and traces for 14
  loco workspace update my-workspace --log-retention-days 7 --trace-retention-days 14

  # Return metrics to the organization's limit
  loco workspace update my-workspace --metric-retention-days 0`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			name, err := cmd.Flags().GetString("name")
			if err != nil {
				return fmt.Errorf("failed to get name flag: %w", err)
			}
			description, err := cmd.Flags().GetString("description")
			if err != nil {
				return fmt.Errorf("failed to get description flag: %w", err)
			}

			updateReq := &workspacev1.UpdateWorkspaceRequest{}
			retention := false
			for flag, field := range map[string]**int32{
				"log-retention-days":    &updateReq.LogRetentionDays,
				"trace-retention-days":  &updateReq.TraceRetentionDays,
				"metric-retention-days": &updateReq.MetricRetentionDays,
			} {
				if !cmd.Flags().Changed(flag) {
					continue
				}
				days, flagErr := cmd.Flags().GetInt32(flag)
				if flagErr != nil {
					return fmt.Errorf("failed to get %s flag: %w", flag, flagErr)
				}
				*field = &days
				retention = true
			}

			if !cmd.Flags().Changed("name") && !cmd.Flags().Changed("description") && !retention {
				return errNoUpdateFlags
			}

			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			workspace, err := findWorkspace(cmd, target.Clients, args[0])
			if err != nil {
				return err
			}
			updateReq.WorkspaceId = workspace.GetId()

			if cmd.Flags().Changed("name") {
				updateReq.Name = &name
			}
			if cmd.Flags().Changed("description") {
				updateReq.Description = &description
			}

			_, err = target.Clients.Workspace.UpdateWorkspace(ctx, updateReq)
			if err != nil {
				return fmt.Errorf("failed to update workspace: %w", err)
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Workspace updated successfully.\n")
			if err != nil || !retention {
				return err
			}
			getReq := &workspacev1.GetWorkspaceRequest{WorkspaceId: workspace.GetId()}
			resp, err := target.Clients.Workspace.GetWorkspace(ctx, getReq)
			if err != nil {
				return fmt.Errorf("failed to get workspace: %w", err)
			}
			return printRetention(cmd, resp.GetWorkspace())
		},
	}

	cmdutil.AddOrgFlags(cmd)
	cmd.Flags().String("name", "", "New name for the workspace")
	cmd.Flags().String("description", "", "New description for the workspace")
	for _, signal := range []string{"log", "trace", "metric"} {
		cmd.Flags().Int32(
			signal+"-retention-days", 0, "Days to keep the workspace's "+signal+"s; 0 uses the organization's limit",
		)
	}

	return cmd
}

// printRetention prints each signal's own days, or "-" without one, and the days that apply.
func printRetention(cmd *cobra.Command, workspace *workspacev1.Workspace) error {
	own := cmp.Or(workspace.GetRetention(), &workspacev1.RetentionDays{})
	applied := cmp.Or(workspace.GetEffectiveRetention(), &workspacev1.RetentionDays{})
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SIGNAL\tWORKSPACE\tAPPLIED")
	fmt.Fprintf(w, "logs\t%s\t%s\n", formatDays(own.LogDays), formatDays(applied.LogDays))
	fmt.Fprintf(w, "traces\t%s\t%s\n", formatDays(own.TraceDays), formatDays(applied.TraceDays))
	fmt.Fprintf(w, "metrics\t%s\t%s\n", formatDays(own.MetricDays), formatDays(applied.MetricDays))
	return w.Flush()
}

func formatDays(days *int32) string {
	if days == nil {
		return "-"
	}
	return fmt.Sprintf("%d days", *days)
}
