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

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/logstream"
	"github.com/team-loco/loco/internal/session"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	defaultBuildListSize = 20
	finishedLogMargin    = time.Minute
	imageDeletedSuffix   = " (deleted)"
)

type buildsDeps struct {
	LoadSessionConfig func() (*session.SessionConfig, error)
	LoadLocoConfig    func(path string) (*config.LoadedConfig, error)
	NewAPIClient      func(host, token string) *client.Client
	Clients           platformClients
	Stdout            io.Writer
}

type buildsSession struct {
	deps   buildsDeps
	host   string
	token  string
	ctx    context.Context
	cancel context.CancelFunc
}

func BuildBuildsCmd() *cobra.Command {
	clients := defaultPlatformClients()
	deps := buildsDeps{
		LoadSessionConfig: session.Load,
		LoadLocoConfig:    config.Load,
		NewAPIClient:      client.NewClient,
		Clients:           clients,
		Stdout:            os.Stdout,
	}
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

func openBuildsSession(cmd *cobra.Command, deps buildsDeps) (*buildsSession, error) {
	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return nil, err
	}
	locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return nil, err
	}
	parent := cmd.Context()
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt)
	return &buildsSession{deps: deps, host: host, token: locoToken.Token, ctx: ctx, cancel: cancel}, nil
}

func (s *buildsSession) authorize(req connect.AnyRequest) {
	authorization := authHeaderFor(s.token)
	req.Header().Set("Authorization", authorization)
}

func (s *buildsSession) follower() *buildFollower {
	return &buildFollower{
		clients: s.deps.Clients,
		host:    s.host,
		token:   s.token,
		out:     &syncWriter{out: s.deps.Stdout},
	}
}

func (s *buildsSession) getBuild(buildID string) (*buildv1.Build, error) {
	return s.follower().getBuild(s.ctx, buildID)
}

func (s *buildsSession) workspaceOf(build *buildv1.Build) (string, error) {
	resourceID := build.GetResourceId()
	req := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
	})
	s.authorize(req)
	resources := s.deps.Clients.Resources(s.host)
	resp, err := resources.GetResource(s.ctx, req)
	if err != nil {
		return "", fmt.Errorf("get the build's service: %w", err)
	}
	return resp.Msg.GetResource().GetWorkspaceId(), nil
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
		Use:   "list [service]",
		Short: "List a service's builds, newest first",
		Long: `List a service's builds, newest first. Without a service name, lists the builds of the
service named in loco.toml.

Examples:
  loco builds list
  loco builds list myapp --limit 50 --output json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildsList(cmd, deps, args)
		},
	}
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().StringP("config", "c", "", "Path to loco.toml, read when no service is named")
	cmd.Flags().Int32("limit", defaultBuildListSize, "Number of builds to show (up to 200)")
	cmd.Flags().StringP("output", "o", outputText, "Output format (text, json)")
	return cmd
}

func serviceName(cmd *cobra.Command, deps buildsDeps, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	configPath, err := cmdutil.GetLocoTomlPath(cmd)
	if err != nil {
		return "", err
	}
	loaded, err := deps.LoadLocoConfig(configPath)
	if err != nil {
		return "", fmt.Errorf("name a service or run this next to its loco.toml: %w", err)
	}
	name := loaded.Config.Metadata.Name
	if name == "" {
		return "", fmt.Errorf("%s does not name a service; pass the service name", configPath)
	}
	return name, nil
}

func runBuildsList(cmd *cobra.Command, deps buildsDeps, args []string) error {
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
	name, err := serviceName(cmd, deps, args)
	if err != nil {
		return err
	}
	s, err := openBuildsSession(cmd, deps)
	if err != nil {
		return err
	}
	defer s.cancel()

	apiClient := deps.NewAPIClient(s.host, s.token)
	workspaceID, err := cmdutil.ResolveWorkspaceID(s.ctx, cmd, deps.LoadSessionConfig, apiClient)
	if err != nil {
		return err
	}
	resourceClient := deps.Clients.Resources(s.host)
	authHeader := authHeaderFor(s.token)
	resource, err := getResourceByName(s.ctx, resourceClient, authHeader, workspaceID, name)
	if err != nil {
		return err
	}

	resourceID := resource.GetId()
	req := connect.NewRequest(&buildv1.ListBuildsRequest{ResourceId: resourceID, PageSize: limit})
	s.authorize(req)
	buildClient := deps.Clients.Builds(s.host)
	resp, err := buildClient.ListBuilds(s.ctx, req)
	if err != nil {
		return fmt.Errorf("list builds: %w", err)
	}
	builds := resp.Msg.GetBuilds()

	if output == outputJSON {
		return writeJSON(deps.Stdout, resp.Msg)
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
			s, err := openBuildsSession(cmd, deps)
			if err != nil {
				return err
			}
			defer s.cancel()
			build, err := s.getBuild(args[0])
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
	s, err := openBuildsSession(cmd, deps)
	if err != nil {
		return err
	}
	defer s.cancel()
	build, err := s.getBuild(buildID)
	if err != nil {
		return err
	}
	workspaceID, err := s.workspaceOf(build)
	if err != nil {
		return err
	}

	status := build.GetStatus()
	if opts.follow && !buildFinished(status) {
		follower := s.follower()
		follower.emit = opts.emit
		follower.quietState = opts.json
		_, followErr := follower.follow(s.ctx, build, workspaceID)
		return followErr
	}

	clusterID := build.GetClusterId()
	if clusterID == "" {
		_, err = fmt.Fprintf(deps.Stdout, "Build %s has not started on a cluster, so it has no logs yet.\n", buildID)
		return err
	}
	access := deps.Clients.Access(s.host)
	proxies, err := logstream.Proxies(s.ctx, access, s.token, workspaceID, clusterID)
	if err != nil {
		return fmt.Errorf("build logs are unavailable: %w", err)
	}
	start := build.GetCreatedAt().AsTime()
	end := time.Now()
	if build.GetFinishedAt() != nil {
		end = build.GetFinishedAt().AsTime().Add(finishedLogMargin)
	}
	filter := logstream.BuildFilter(workspaceID, buildID)
	logs := deps.Clients.logClient(s.token)
	window := logstream.Window{Start: start, End: end}
	streamErr := logs.Stream(s.ctx, proxies, filter, window, opts.emit)
	if s.ctx.Err() != nil {
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
			s, err := openBuildsSession(cmd, deps)
			if err != nil {
				return err
			}
			defer s.cancel()
			req := connect.NewRequest(&buildv1.CancelBuildRequest{BuildId: args[0]})
			s.authorize(req)
			builds := deps.Clients.Builds(s.host)
			resp, err := builds.CancelBuild(s.ctx, req)
			if err != nil {
				return fmt.Errorf("cancel build %s: %w", args[0], err)
			}
			canceled := resp.Msg.GetBuild()
			description := describeBuild(canceled)
			_, err = fmt.Fprintln(deps.Stdout, description)
			return err
		},
	}
}
