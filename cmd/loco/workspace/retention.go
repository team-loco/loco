package workspace

import (
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/team-loco/loco/cmd/loco/cmdutil"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

var (
	errNoSignals     = errors.New("name at least one signal to set, such as --logs 30")
	errUnknownSignal = errors.New("unknown signal")
)

// retentionSignal is one signal a workspace keeps for a number of days.
type retentionSignal struct {
	name  string
	field string
	days  func(*workspacev1.RetentionDays) **int32
}

var retentionSignals = []retentionSignal{
	{name: "logs", field: "log_days", days: func(d *workspacev1.RetentionDays) **int32 { return &d.LogDays }},
	{name: "traces", field: "trace_days", days: func(d *workspacev1.RetentionDays) **int32 { return &d.TraceDays }},
	{
		name: "metrics", field: "metric_days",
		days: func(d *workspacev1.RetentionDays) **int32 { return &d.MetricDays },
	},
}

func buildRetentionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Show or set how long a workspace keeps its logs, traces and metrics",
		Long: `Show or set how many days a workspace keeps its logs, traces and metrics. Each value is at
most the organization's retention limit; a signal without one keeps the limit. A change applies
to telemetry ingested after it. Setting and unsetting require workspace admin.`,
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
			resp, err := target.Clients.Workspace.GetWorkspaceRetention(
				cmd.Context(), &workspacev1.GetWorkspaceRetentionRequest{WorkspaceId: args[0]},
			)
			if err != nil {
				return fmt.Errorf("get workspace retention: %w", err)
			}
			return printRetention(cmd, resp.GetRetention())
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
			settings := &workspacev1.RetentionDays{}
			var paths []string
			for _, signal := range retentionSignals {
				if !cmd.Flags().Changed(signal.name) {
					continue
				}
				days, err := cmd.Flags().GetInt32(signal.name)
				if err != nil {
					return err
				}
				*signal.days(settings) = &days
				paths = append(paths, signal.field)
			}
			if len(paths) == 0 {
				return errNoSignals
			}
			return setRetention(cmd, args[0], settings, paths)
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
			paths := make([]string, 0, len(args)-1)
			for _, name := range args[1:] {
				signal, ok := signalNamed(name)
				if !ok {
					return fmt.Errorf("%w %q; use logs, traces or metrics", errUnknownSignal, name)
				}
				paths = append(paths, signal.field)
			}
			return setRetention(cmd, args[0], &workspacev1.RetentionDays{}, paths)
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

func setRetention(cmd *cobra.Command, workspaceID string, settings *workspacev1.RetentionDays, paths []string) error {
	target, err := cmdutil.ResolveLogin(cmd)
	if err != nil {
		return err
	}
	resp, err := target.Clients.Workspace.SetWorkspaceRetention(
		cmd.Context(),
		&workspacev1.SetWorkspaceRetentionRequest{
			WorkspaceId: workspaceID,
			Settings:    settings,
			UpdateMask:  &fieldmaskpb.FieldMask{Paths: paths},
		},
	)
	if err != nil {
		return fmt.Errorf("set workspace retention: %w", err)
	}
	return printRetention(cmd, resp.GetRetention())
}

func printRetention(cmd *cobra.Command, retention *workspacev1.WorkspaceRetention) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SIGNAL\tWORKSPACE\tORGANIZATION LIMIT\tAPPLIED")
	for _, signal := range retentionSignals {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", signal.name,
			signal.format(retention.GetSettings()),
			signal.format(retention.GetLimits()),
			signal.format(retention.GetEffective()))
	}
	return w.Flush()
}

// format prints the signal's days in a response, or "-" when the response leaves it unset.
func (s retentionSignal) format(days *workspacev1.RetentionDays) string {
	if days == nil || *s.days(days) == nil {
		return "-"
	}
	return fmt.Sprintf("%d days", **s.days(days))
}
