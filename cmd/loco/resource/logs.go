package resource

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/logstream"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	outputText       = "text"
	defaultLogLines  = 100
	defaultLogWindow = time.Hour
)

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "View service logs",
		Long: `Show a service's logs from every region it runs in, oldest first.

Examples:
  loco resource logs myapp
  loco resource logs myapp --follow
  loco resource logs myapp --since 6h --lines 500 --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResourceLogs(cmd, args[0])
		},
	}

	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().BoolP("follow", "f", false, "Keep printing new log lines")
	cmd.Flags().Int32P("lines", "n", defaultLogLines, "Number of recent lines to show (0 = every line in --since)")
	cmd.Flags().Duration("since", defaultLogWindow, "How far back to look, up to 24h")
	cmd.Flags().StringP("output", "o", outputText, "Output format (text, json)")
	cmd.Flags().String("host", "", "API host URL")

	return cmd
}

type logOptions struct {
	follow bool
	json   bool
	lines  int32
	since  time.Duration
	emit   logstream.Emit
}

func readLogOptions(cmd *cobra.Command, out io.Writer) (logOptions, error) {
	var opts logOptions
	var err error
	if opts.follow, err = cmd.Flags().GetBool("follow"); err != nil {
		return opts, fmt.Errorf("error reading follow flag: %w", err)
	}
	if cmd.Flags().Lookup("lines") != nil {
		if opts.lines, err = cmd.Flags().GetInt32("lines"); err != nil {
			return opts, fmt.Errorf("error reading lines flag: %w", err)
		}
	}
	if cmd.Flags().Lookup("since") != nil {
		if opts.since, err = cmd.Flags().GetDuration("since"); err != nil {
			return opts, fmt.Errorf("error reading since flag: %w", err)
		}
	}
	if opts.lines < 0 {
		return opts, errors.New("--lines must not be negative")
	}
	if opts.since > logstream.MaxRange {
		return opts, errors.New("the log proxy reads at most 24h back; lower --since")
	}
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return opts, fmt.Errorf("error reading output flag: %w", err)
	}
	opts.json = output == outputJSON
	opts.emit, err = logEmitter(out, output)
	return opts, err
}

func logEmitter(out io.Writer, output string) (logstream.Emit, error) {
	switch output {
	case outputJSON:
		return func(entry *observabilityv1.LogEntry) error {
			line, err := protojson.Marshal(entry)
			if err != nil {
				return fmt.Errorf("encode log entry: %w", err)
			}
			_, err = fmt.Fprintln(out, string(line))
			return err
		}, nil
	case outputText:
		return func(entry *observabilityv1.LogEntry) error {
			ts := entry.GetTimestamp().AsTime().UTC().Format(time.RFC3339)
			body := entry.GetBody()
			line := trimNewline(body)
			_, err := fmt.Fprintf(out, "%s  %s\n", ts, line)
			return err
		}, nil
	default:
		return nil, fmt.Errorf("invalid output format %q: use text or json", output)
	}
}

func runResourceLogs(cmd *cobra.Command, name string) error {
	opts, err := readLogOptions(cmd, cmd.OutOrStdout())
	if err != nil {
		return err
	}
	parent := cmd.Context()
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()

	t, err := cmdutil.ResolveTarget(ctx, cmd)
	if err != nil {
		return err
	}
	resource, err := t.Service(ctx, name)
	if err != nil {
		return err
	}

	filter := logstream.Filter{WorkspaceID: t.WorkspaceID, ResourceIDs: []string{resource.GetId()}}
	end := time.Now()
	start := end.Add(-opts.since)
	logs := logClient(t.Host, t.Token)
	window := logstream.Window{Start: start, End: end, Limit: opts.lines, Follow: opts.follow}
	streamErr := logs.Stream(ctx, filter, window, opts.emit)
	if ctx.Err() != nil {
		return nil
	}
	return streamErr
}
