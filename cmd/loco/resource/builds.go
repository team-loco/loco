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
	"github.com/team-loco/loco/cmd/internal/sourcebuild"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	finishedLogMargin  = time.Minute
	imageDeletedSuffix = " (deleted)"
	shortCommitLength  = 7
)

func BuildBuildsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "builds",
		Short: "Inspect and cancel source builds",
		Long:  "Commands for the builds `loco deploy` starts when it deploys from source.",
	}
	cmd.PersistentFlags().String("host", "", "API host URL")
	cmd.AddCommand(
		newBuildsListCmd(),
		newBuildsGetCmd(),
		newBuildsLogsCmd(),
		newBuildsCancelCmd(),
	)
	return cmd
}

func follower(cmd *cobra.Command, t cmdutil.Target) *sourcebuild.Follower {
	return sourcebuild.NewFollower(t.Clients, cmd.OutOrStdout())
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

func newBuildsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <service>",
		Short: "List a service's builds, newest first",
		Long: `List a service's builds, newest first.

Examples:
  loco builds list myapp
  loco builds list myapp --limit 50 --output json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildsList(cmd, args[0])
		},
	}
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().Int32("limit", 0, "Maximum number of builds to show (0 = all)")
	cmd.Flags().StringP("output", "o", outputText, "Output format (text, json)")
	return cmd
}

func runBuildsList(cmd *cobra.Command, name string) error {
	output, err := readOutput(cmd)
	if err != nil {
		return err
	}
	limit, err := cmd.Flags().GetInt32("limit")
	if err != nil {
		return fmt.Errorf("error reading limit flag: %w", err)
	}
	if limit < 0 {
		return errors.New("--limit must be nonnegative")
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

	var fetched int32
	builds, err := cmdutil.Paginate(func(token string) ([]*buildv1.Build, string, error) {
		var size int32
		if limit > 0 {
			size = limit - fetched
		}
		req := &buildv1.ListBuildsRequest{ResourceId: resource.GetId(), PageSize: size, PageToken: token}
		resp, listErr := t.Clients.Build.ListBuilds(ctx, req)
		if listErr != nil {
			return nil, "", listErr
		}
		fetched += int32(len(resp.GetBuilds()))
		next := resp.GetNextPageToken()
		if limit > 0 && fetched >= limit {
			next = ""
		}
		return resp.GetBuilds(), next, nil
	})
	if err != nil {
		return fmt.Errorf("list builds: %w", err)
	}

	if output == outputJSON {
		return writeJSON(cmd.OutOrStdout(), &buildv1.ListBuildsResponse{Builds: builds})
	}
	if len(builds) == 0 {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s has no builds.\n", name)
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tCOMMIT\tCREATED\tDURATION\tIMAGE DIGEST")
	for _, build := range builds {
		created := build.GetCreatedAt().AsTime().UTC().Format(time.RFC3339)
		duration := sourcebuild.Duration(build)
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
		label := sourcebuild.StatusLabel(status)
		id := build.GetId()
		commit := shortCommit(build)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", id, label, commit, created, duration, digest)
	}
	return w.Flush()
}

func shortCommit(build *buildv1.Build) string {
	commit := build.GetGitCommit()
	if commit == "" {
		return "-"
	}
	return commit[:min(len(commit), shortCommitLength)]
}

func newBuildsGetCmd() *cobra.Command {
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
			build, err := follower(cmd, t).GetBuild(ctx, args[0])
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(cmd.OutOrStdout(), build)
			}
			return writeBuildDetails(cmd.OutOrStdout(), build)
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
		{"Status", sourcebuild.StatusLabel(status)},
		{"Message", build.GetMessage()},
		{"Dockerfile", build.GetDockerfilePath()},
		{"Commit", build.GetGitCommit()},
		{"Branch", build.GetGitBranch()},
		{"Commit message", build.GetGitCommitMessage()},
		{"Source size", sourcebuild.FormatBytes(sourceSize)},
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

func newBuildsLogsCmd() *cobra.Command {
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
			return runBuildsLogs(cmd, args[0])
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "Follow the build until it finishes")
	cmd.Flags().StringP("output", "o", outputText, "Output format (text, json)")
	return cmd
}

func runBuildsLogs(cmd *cobra.Command, buildID string) error {
	opts, err := readLogOptions(cmd, cmd.OutOrStdout())
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	t, err := cmdutil.ResolveLogin(cmd)
	if err != nil {
		return err
	}
	follow := follower(cmd, t)
	build, err := follow.GetBuild(ctx, buildID)
	if err != nil {
		return err
	}
	workspaceID, err := workspaceOf(ctx, t, build)
	if err != nil {
		return err
	}

	status := build.GetStatus()
	if opts.follow && !sourcebuild.Finished(status) {
		follow.Emit = opts.emit
		follow.QuietState = opts.json
		_, followErr := follow.Follow(ctx, build, workspaceID)
		return followErr
	}

	start := build.GetCreatedAt().AsTime()
	end := time.Now()
	if build.GetFinishedAt() != nil {
		end = build.GetFinishedAt().AsTime().Add(finishedLogMargin)
	}
	filter := logstream.BuildFilter(workspaceID, buildID)
	logs := &logstream.Client{Proxy: t.Clients.ObservabilityProxy}
	window := logstream.Window{Start: start, End: end}
	streamErr := logs.Stream(ctx, filter, window, opts.emit)
	if ctx.Err() != nil {
		return nil
	}
	return streamErr
}

func newBuildsCancelCmd() *cobra.Command {
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
			description := sourcebuild.Describe(canceled)
			_, err = fmt.Fprintln(cmd.OutOrStdout(), description)
			return err
		},
	}
}
