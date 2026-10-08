package resource

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/session"
	"github.com/team-loco/loco/internal/ui"
)

type deployDeps struct {
	LoadSessionConfig   func() (*session.SessionConfig, error)
	LoadLocoConfig      func(path string) (*config.LoadedConfig, error)
	NewAPIClient        func(host, token string) *client.Client
	NewResourceClient   func(host string) resourcev1connect.ResourceServiceClient
	NewDeploymentClient func(host string) deploymentv1connect.DeploymentServiceClient
	NewDomainClient     func(host string) domainv1connect.DomainServiceClient
	Clients             platformClients
	SelectFromList      func(title string, options []ui.SelectOption) (any, error)
	Interactive         func() bool
	Stdout              io.Writer
}

func BuildDeployCmd() *cobra.Command {
	clients := defaultPlatformClients()
	deps := deployDeps{
		LoadSessionConfig: session.Load,
		LoadLocoConfig:    config.Load,
		NewAPIClient:      client.NewClient,
		NewResourceClient: func(host string) resourcev1connect.ResourceServiceClient {
			return resourcev1connect.NewResourceServiceClient(httputil.NewHTTPClient(), host)
		},
		NewDeploymentClient: func(host string) deploymentv1connect.DeploymentServiceClient {
			return deploymentv1connect.NewDeploymentServiceClient(httputil.NewHTTPClient(), host)
		},
		NewDomainClient: func(host string) domainv1connect.DomainServiceClient {
			return domainv1connect.NewDomainServiceClient(httputil.NewHTTPClient(), host)
		},
		Clients:        clients,
		SelectFromList: ui.SelectFromList,
		Interactive:    stdoutIsTerminal,
		Stdout:         os.Stdout,
	}
	return newDeployCmd(deps)
}

func newDeployCmd(deps deployDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy <name>",
		Short: "Deploy a service to Loco",
		Long: `Deploy a service to Loco.

Reads loco.toml from the current directory, or from the path given with --config.
Without --image, packs the directory that holds loco.toml, builds it on Loco with the
Dockerfile named in loco.toml, and deploys the result to every region in loco.toml.
The archive honors .dockerignore and never contains .git, .env or .env.* files.
Ctrl-C while the build runs detaches without canceling it.

Examples:
  loco deploy myapp
  loco deploy myapp --env production --wait
  loco deploy myapp --image ghcr.io/acme/myapp:v1
  loco deploy myapp --image ghcr.io/acme/myapp:v1 --config ./loco.toml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(cmd, deps, args[0])
		},
	}

	cmd.Flags().StringP("config", "c", "", "Path to loco.toml config file (optional)")
	cmd.Flags().String("org", "", "Organization name")
	cmd.Flags().String("workspace", "", "Workspace name")
	cmd.Flags().String("env", "", "Environment to deploy to (defaults to the workspace's only environment)")
	cmd.Flags().StringP("image", "i", "", "Public image reference to deploy instead of building from source")
	cmd.Flags().String("host", "", "API host URL")
	cmd.Flags().Bool("wait", false, "Wait until the deployment runs in every region")

	return cmd
}

func runDeploy(cmd *cobra.Command, deps deployDeps, name string) error {
	imageName, err := cmd.Flags().GetString("image")
	if err != nil {
		return fmt.Errorf("failed to get image flag: %w", err)
	}
	wait, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return fmt.Errorf("failed to get wait flag: %w", err)
	}

	host, err := cmdutil.GetHost(cmd)
	if err != nil {
		return err
	}
	locoToken, err := cmdutil.GetCurrentLocoToken(cmd)
	if err != nil {
		return err
	}
	authHeader := authHeaderFor(locoToken.Token)

	parent := cmd.Context()
	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()

	apiClient := deps.NewAPIClient(host, locoToken.Token)
	workspaceID, err := resolveWorkspaceID(ctx, cmd, deps.LoadSessionConfig, apiClient)
	if err != nil {
		return err
	}

	loadedCfg, err := loadDeployConfig(cmd, deps)
	if err != nil {
		return err
	}
	loadedCfg.Config.Metadata.Name = name
	if validateErr := config.Validate(loadedCfg.Config); validateErr != nil {
		return fmt.Errorf("config validation failed: %w", validateErr)
	}
	config.FillSensibleDefaults(loadedCfg.Config)

	interactive := deps.Interactive()
	environmentClient := deps.Clients.Environments(host)
	environmentID, err := resolveEnvironmentID(
		ctx,
		cmd,
		environmentClient,
		deps.SelectFromList,
		interactive,
		authHeader,
		workspaceID,
	)
	if err != nil {
		return err
	}

	resourceClient := deps.NewResourceClient(host)
	domainClient := deps.NewDomainClient(host)
	resource, err := getOrCreateResource(
		ctx,
		resourceClient,
		domainClient,
		deps.SelectFromList,
		authHeader,
		workspaceID,
		loadedCfg.Config,
	)
	if err != nil {
		return err
	}
	resourceID := resource.GetId()

	source := imageSource(imageName)
	if imageName == "" {
		dockerfile, dockerfileErr := dockerfileInContext(loadedCfg.ProjectPath, loadedCfg.Config.Build.DockerfilePath)
		if dockerfileErr != nil {
			return dockerfileErr
		}
		builder := &sourceBuilder{follower: &buildFollower{
			clients: deps.Clients,
			host:    host,
			token:   locoToken.Token,
			out:     &syncWriter{out: deps.Stdout},
		}}
		build, buildErr := builder.build(ctx, resourceID, workspaceID, loadedCfg.ProjectPath, dockerfile)
		if buildErr != nil {
			return buildErr
		}
		buildID := build.GetId()
		source = dockerfileSource(buildID)
	}

	deploymentClient := deps.NewDeploymentClient(host)
	req := deploymentRequest{
		resourceID:    resourceID,
		environmentID: environmentID,
		source:        source,
		cfg:           loadedCfg.Config,
		wait:          wait,
	}
	if deployErr := createDeployments(
		ctx,
		deploymentClient,
		authHeader,
		req,
		interactive,
		deps.Stdout,
	); deployErr != nil {
		return deployErr
	}

	successMsg := "\nDeployment scheduled."
	if wait {
		successMsg = "\nService deployed."
	}
	s := lipgloss.NewStyle().Bold(true).Foreground(ui.Ok).Render(successMsg)
	if _, printErr := lipgloss.Fprintln(deps.Stdout, s); printErr != nil {
		return printErr
	}

	reachText := reachability(resource)
	reach := lipgloss.NewStyle().Foreground(ui.Fg).Render(reachText)
	if _, printErr := lipgloss.Fprintln(deps.Stdout, reach); printErr != nil {
		return printErr
	}

	tip := lipgloss.NewStyle().
		Foreground(ui.Fg3).
		Render("Check on it with `loco resource status " + name + "`")
	_, err = lipgloss.Fprintln(deps.Stdout, tip)
	return err
}

func reachability(resource *resourcev1.Resource) string {
	url := publicURL(resource)
	if url == "" {
		return resource.GetName() + " has no public URL and takes no internet traffic."
	}
	return "Public URL: " + url
}

func loadDeployConfig(cmd *cobra.Command, deps deployDeps) (*config.LoadedConfig, error) {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to get config flag: %w", err)
	}
	if configPath == "" {
		configPath = "loco.toml"
	}

	loadedCfg, err := deps.LoadLocoConfig(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no loco.toml found at %s", configPath)
		}
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	return loadedCfg, nil
}

func getOrCreateResource(
	ctx context.Context,
	resourceClient resourcev1connect.ResourceServiceClient,
	domainClient domainv1connect.DomainServiceClient,
	selectFromList func(title string, options []ui.SelectOption) (any, error),
	authHeader string,
	workspaceID string,
	cfg *config.LocoConfig,
) (*resourcev1.Resource, error) {
	nameKey := &resourcev1.GetResourceNameKey{WorkspaceId: workspaceID, Name: cfg.Metadata.Name}
	existing, err := getResource(ctx, resourceClient, authHeader, &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{NameKey: nameKey},
	})
	if err == nil {
		slog.Debug("found existing resource", "resource_id", existing.GetId(), "name", existing.GetName())
		return existing, nil
	}

	if connect.CodeOf(err) != connect.CodeNotFound {
		return nil, fmt.Errorf("failed to get resource '%s': %w", cfg.Metadata.Name, err)
	}

	slog.Info("no existing resource found, creating new one")

	domainInput, err := resolveDomainInput(ctx, domainClient, selectFromList, authHeader, cfg)
	if err != nil {
		return nil, err
	}

	resourceSpec, err := configToResourceSpec(cfg, "v1")
	if err != nil {
		return nil, fmt.Errorf("failed to convert config to resource spec: %w", err)
	}

	createReq := connect.NewRequest(&resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID,
		Name:        cfg.Metadata.Name,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Domain:      domainInput,
		Spec:        resourceSpec,
	})
	createReq.Header().Set("Authorization", authHeader)

	createResp, err := resourceClient.CreateResource(ctx, createReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	resourceID := createResp.Msg.GetResourceId()
	slog.Debug("created resource", "resourceId", resourceID)
	created, err := getResource(ctx, resourceClient, authHeader, &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get created resource: %w", err)
	}
	return created, nil
}

func getResource(
	ctx context.Context,
	resourceClient resourcev1connect.ResourceServiceClient,
	authHeader string,
	msg *resourcev1.GetResourceRequest,
) (*resourcev1.Resource, error) {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", authHeader)
	resp, err := resourceClient.GetResource(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetResource(), nil
}
