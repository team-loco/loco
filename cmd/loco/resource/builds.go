package resource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/logstream"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	defaultBuildListSize = 20
	finishedLogMargin    = time.Minute
	imageDeletedSuffix   = " (deleted)"
)

type buildsDeps struct {
	Clients platformClients
	Stdout  io.Writer
}

func BuildBuildsCmd() *cobra.Command {
	clients := defaultPlatformClients()
	deps := buildsDeps{Clients: clients, Stdout: os.Stdout}
	return newBuildsCmd(deps)
}

func newBuildsCmd(deps buildsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "builds",
		Short: "Inspect and cancel source builds",
		Long:  "Commands for the builds `loco deploy` starts when it deploys from source.",
	}
	cmd.PersistentFlags().String("host", "", "API host URL")
	cmd.AddCommand(
		newBuildsListCmd(deps),
		newBuildsGetCmd(deps),
		newBuildsLogsCmd(deps),
		newBuildsCancelCmd(deps),
	)
	return cmd
}

func follower(deps buildsDeps, t cmdutil.Target) *buildFollower {
	return &buildFollower{
		clients: deps.Clients,
		host:    t.Host,
		token:   t.Token,
		out:     &syncWriter{out: deps.Stdout},
	}
}

func workspaceOf(ctx context.Context, t cmdutil.Target, build *buildv1.Build) (string, error) {
	resourceID := build.GetResourceId()
	req := &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
	}

	resp, err := t.Clients.Resource.GetResource(ctx, req)
	if err != nil {
		return "", fmt.Errorf("get the build's service: %w", err)
	}
	return resp.GetResource().GetWorkspaceId(), nil
}

func readOutput(cmd *cobra.Command) (string, error) {
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return "", fmt.Errorf("error reading output flag: %w", err)
	}
	switch output {
	case outputText:
		return output, nil
	case outputJSON:
		return output, nil
	default:
		return "", fmt.Errorf("invalid output format %q: use text or json", output)
	}
}

func writeJSON(out io.Writer, msg proto.Message) error {
	data, err := protojson.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

func newBuildsListCmd(deps buildsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <service>",
		Short: "List a service's builds, newest first",
		Long: `List a service's builds, newest first.

Examples:
  loco builds list myapp
  loco builds list myapp --limit 50 --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildsList(cmd, deps, args[0])
		},
	}
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().Int32("limit", defaultBuildListSize, "Number of builds to show (up to 200)")
	cmd.Flags().StringP("output", "o", outputText, "Output format (text, json)")
	return cmd
}

func runBuildsList(cmd *cobra.Command, deps buildsDeps, name string) error {
	output, err := readOutput(cmd)
	if err != nil {
		return err
	}
	limit, err := cmd.Flags().GetInt32("limit")
	if err != nil {
		return fmt.Errorf("error reading limit flag: %w", err)
	}
	if limit < 1 || limit > 200 {
		return errors.New("--limit must be between 1 and 200")
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	t, err := cmdutil.ResolveTarget(ctx, cmd)
	if err != nil {
		return err
	}
	resource, err := t.Service(ctx, name)
	if err != nil {
		return err
	}

	resourceID := resource.GetId()
	req := &buildv1.ListBuildsRequest{ResourceId: resourceID, PageSize: limit}

	resp, err := t.Clients.Build.ListBuilds(ctx, req)
	if err != nil {
		return fmt.Errorf("list builds: %w", err)
	}
	builds := resp.GetBuilds()

	if output == outputJSON {
		return writeJSON(deps.Stdout, resp)
	}
	if len(builds) == 0 {
		_, err = fmt.Fprintf(deps.Stdout, "%s has no builds.\n", name)
		return err
	}
	w := tabwriter.NewWriter(deps.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tCREATED\tDURATION\tIMAGE DIGEST")
	for _, build := range builds {
		created := build.GetCreatedAt().AsTime().UTC().Format(time.RFC3339)
		duration := buildDuration(build)
		if duration == "" {
			duration = "-"
		}
		digest := build.GetImageDigest()
		if digest == "" {
			digest = "-"
		}
		if build.GetImageDeletedAt() != nil {
			digest += imageDeletedSuffix
		}
		status := build.GetStatus()
		label := buildStatusLabel(status)
		id := build.GetId()
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, label, created, duration, digest)
	}
	return w.Flush()
}

func newBuildsGetCmd(deps buildsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <build-id>",
		Short: "Show one build",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			output, err := readOutput(cmd)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			t, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			build, err := follower(deps, t).getBuild(ctx, args[0])
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(deps.Stdout, build)
			}
			return writeBuildDetails(deps.Stdout, build)
		},
	}
	cmd.Flags().StringP("output", "o", outputText, "Output format (text, json)")
	return cmd
}

func writeBuildDetails(out io.Writer, build *buildv1.Build) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	status := build.GetStatus()
	sourceSize := build.GetSourceSize()
	created := build.GetCreatedAt().AsTime().UTC().Format(time.RFC3339)
	rows := [][2]string{
		{"ID", build.GetId()},
		{"Service", build.GetResourceId()},
		{"Status", buildStatusLabel(status)},
		{"Message", build.GetMessage()},
		{"Dockerfile", build.GetDockerfilePath()},
		{"Source size", formatBytes(sourceSize)},
		{"Image", build.GetImageRepository()},
		{"Image digest", build.GetImageDigest()},
		{"Created", created},
	}
	if build.GetStartedAt() != nil {
		started := build.GetStartedAt().AsTime().UTC().Format(time.RFC3339)
		rows = append(rows, [2]string{"Started", started})
	}
	if build.GetFinishedAt() != nil {
		finished := build.GetFinishedAt().AsTime().UTC().Format(time.RFC3339)
		rows = append(rows, [2]string{"Finished", finished})
	}
	if build.GetImageDeletedAt() != nil {
		deleted := build.GetImageDeletedAt().AsTime().UTC().Format(time.RFC3339)
		rows = append(rows, [2]string{"Image deleted", deleted})
	}
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		fmt.Fprintf(w, "%s:\t%s\n", row[0], row[1])
	}
	return w.Flush()
}

func newBuildsLogsCmd(deps buildsDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <build-id>",
		Short: "Show a build's logs",
		Long: `Show a build's logs. With --follow, keeps printing them and the build's status until
the build finishes; Ctrl-C stops following without canceling the build.

Examples:
  loco builds logs 0199b6c4-5d1e-7f00-8000-000000000001
  loco builds logs 0199b6c4-5d1e-7f00-8000-000000000001 --follow`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildsLogs(cmd, deps, args[0])
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "Follow the build until it finishes")
	cmd.Flags().StringP("output", "o", outputText, "Output format (text, json)")
	return cmd
}

func runBuildsLogs(cmd *cobra.Command, deps buildsDeps, buildID string) error {
	opts, err := readLogOptions(cmd, deps.Stdout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	t, err := cmdutil.ResolveLogin(cmd)
	if err != nil {
		return err
	}
	follow := follower(deps, t)
	build, err := follow.getBuild(ctx, buildID)
	if err != nil {
		return err
	}
	workspaceID, err := workspaceOf(ctx, t, build)
	if err != nil {
		return err
	}

	status := build.GetStatus()
	if opts.follow && !buildFinished(status) {
		follow.emit = opts.emit
		follow.quietState = opts.json
		_, followErr := follow.follow(ctx, build, workspaceID)
		return followErr
	}

	start := build.GetCreatedAt().AsTime()
	end := time.Now()
	if build.GetFinishedAt() != nil {
		end = build.GetFinishedAt().AsTime().Add(finishedLogMargin)
	}
	filter := logstream.BuildFilter(workspaceID, buildID)
	logs := deps.Clients.logClient(t.Host, t.Token)
	window := logstream.Window{Start: start, End: end}
	streamErr := logs.Stream(ctx, filter, window, opts.emit)
	if ctx.Err() != nil {
		return nil
	}
	return streamErr
}

func newBuildsCancelCmd(deps buildsDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <build-id>",
		Short: "Cancel a build that has not finished",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			t, err := cmdutil.ResolveLogin(cmd)
			if err != nil {
				return err
			}
			req := &buildv1.CancelBuildRequest{BuildId: args[0]}

			resp, err := t.Clients.Build.CancelBuild(ctx, req)
			if err != nil {
				return fmt.Errorf("cancel build %s: %w", args[0], err)
			}
			canceled := resp.GetBuild()
			description := describeBuild(canceled)
			_, err = fmt.Fprintln(deps.Stdout, description)
			return err
		},
	}
}
