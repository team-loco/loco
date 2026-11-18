package workspace

import (
	"cmp"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

var (
	errNoSignals     = errors.New("name at least one signal to set, such as --logs 30")
	errUnknownSignal = errors.New("unknown signal")
)

// retentionSignal is one signal a workspace keeps for a number of days.
type retentionSignal struct {
	name    string
	days    func(*workspacev1.RetentionDays) *int32
	limit   func(*configv1.CountLimits) int32
	request func(*workspacev1.UpdateWorkspaceRequest) **int32
}

var retentionSignals = []retentionSignal{
	{
		name:    "logs",
		days:    func(d *workspacev1.RetentionDays) *int32 { return d.LogDays },
		limit:   (*configv1.CountLimits).GetLogRetentionDays,
		request: func(r *workspacev1.UpdateWorkspaceRequest) **int32 { return &r.LogRetentionDays },
	},
	{
		name:    "traces",
		days:    func(d *workspacev1.RetentionDays) *int32 { return d.TraceDays },
		limit:   (*configv1.CountLimits).GetTraceRetentionDays,
		request: func(r *workspacev1.UpdateWorkspaceRequest) **int32 { return &r.TraceRetentionDays },
	},
	{
		name:    "metrics",
		days:    func(d *workspacev1.RetentionDays) *int32 { return d.MetricDays },
		limit:   (*configv1.CountLimits).GetMetricRetentionDays,
		request: func(r *workspacev1.UpdateWorkspaceRequest) **int32 { return &r.MetricRetentionDays },
	},
}

func buildRetentionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Show or set how long a workspace keeps its logs, traces and metrics",
		Long: `Show or set how many days a workspace keeps its logs, traces and metrics. Each value is at
most the organization's retention limit; a signal without one keeps the limit. A change applies
to telemetry ingested after it. Setting and unsetting require write access to the workspace.`,
		Example: `  # Show the workspace's retention
  loco workspace retention get <workspace-id>

  # Keep logs for 7 days and traces for 14
  loco workspace retention set <workspace-id> --logs 7 --traces 14

  # Return logs to the organization's limit
  loco workspace retention unset <workspace-id> logs`,
	}
	cmd.AddCommand(buildRetentionGetCmd(), buildRetentionSetCmd(), buildRetentionUnsetCmd())
	return cmd
}

func buildRetentionGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <workspace-id>",
		Short: "Print each signal's workspace value, organization limit and applied retention",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			return printRetention(cmd, target, args[0])
		},
	}
	cmd.Flags().String("host", "", "API host URL")
	return cmd
}

func buildRetentionSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <workspace-id> [--logs <days>] [--traces <days>] [--metrics <days>]",
		Short: "Set how many days the workspace keeps each signal; the others keep their value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &workspacev1.UpdateWorkspaceRequest{WorkspaceId: args[0]}
			changed := false
			for _, signal := range retentionSignals {
				if !cmd.Flags().Changed(signal.name) {
					continue
				}
				days, err := cmd.Flags().GetInt32(signal.name)
				if err != nil {
					return err
				}
				*signal.request(req) = &days
				changed = true
			}
			if !changed {
				return errNoSignals
			}
			return updateRetention(cmd, req)
		},
	}
	cmd.Flags().String("host", "", "API host URL")
	for _, signal := range retentionSignals {
		cmd.Flags().Int32(signal.name, 0, "Days to keep the workspace's "+signal.name)
	}
	return cmd
}

func buildRetentionUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <workspace-id> <signal>...",
		Short: "Return signals (logs, traces, metrics) to the organization's retention limit",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &workspacev1.UpdateWorkspaceRequest{WorkspaceId: args[0]}
			for _, name := range args[1:] {
				signal, ok := signalNamed(name)
				if !ok {
					return fmt.Errorf("%w %q; use logs, traces or metrics", errUnknownSignal, name)
				}
				*signal.request(req) = new(int32(0))
			}
			return updateRetention(cmd, req)
		},
	}
	cmd.Flags().String("host", "", "API host URL")
	return cmd
}

func signalNamed(name string) (retentionSignal, bool) {
	for _, signal := range retentionSignals {
		if signal.name == name {
			return signal, true
		}
	}
	return retentionSignal{}, false
}

func updateRetention(cmd *cobra.Command, req *workspacev1.UpdateWorkspaceRequest) error {
	target, err := cmdutil.ResolveLogin(cmd)
	if err != nil {
		return err
	}
	if _, err = target.Clients.Workspace.UpdateWorkspace(cmd.Context(), req); err != nil {
		return fmt.Errorf("set workspace retention: %w", err)
	}
	return printRetention(cmd, target, req.GetWorkspaceId())
}

func printRetention(cmd *cobra.Command, target cmdutil.Target, workspaceID string) error {
	workspace, err := target.Clients.Workspace.GetWorkspace(
		cmd.Context(), &workspacev1.GetWorkspaceRequest{WorkspaceId: workspaceID},
	)
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	limits, err := target.Clients.Org.GetOrgLimits(
		cmd.Context(), &orgv1.GetOrgLimitsRequest{OrgId: workspace.GetWorkspace().GetOrgId()},
	)
	if err != nil {
		return fmt.Errorf("get organization limits: %w", err)
	}
	own := cmp.Or(workspace.GetWorkspace().GetRetention(), &workspacev1.RetentionDays{})
	effective := cmp.Or(workspace.GetWorkspace().GetEffectiveRetention(), &workspacev1.RetentionDays{})
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SIGNAL\tWORKSPACE\tORGANIZATION LIMIT\tAPPLIED")
	for _, signal := range retentionSignals {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", signal.name,
			formatDays(signal.days(own)),
			formatDays(new(signal.limit(limits.GetLimits().GetLimits()))),
			formatDays(signal.days(effective)))
	}
	return w.Flush()
}

// formatDays prints days, or "-" when the response leaves them unset.
func formatDays(days *int32) string {
	if days == nil {
		return "-"
	}
	return fmt.Sprintf("%d days", *days)
}
