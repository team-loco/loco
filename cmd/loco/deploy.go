package loco

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/loco-team/loco/internal/client"
	"github.com/loco-team/loco/internal/docker"
	"github.com/loco-team/loco/internal/ui"
	"github.com/loco-team/loco/shared"
	"github.com/loco-team/loco/shared/config"
	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	domainv1 "github.com/loco-team/loco/shared/proto/domain/v1"
	"github.com/loco-team/loco/shared/proto/domain/v1/domainv1connect"
	registryv1 "github.com/loco-team/loco/shared/proto/registry/v1"
	registryv1connect "github.com/loco-team/loco/shared/proto/registry/v1/registryv1connect"
	resourcev1 "github.com/loco-team/loco/shared/proto/resource/v1"
	"github.com/loco-team/loco/shared/proto/resource/v1/resourcev1connect"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy/Update an application to Loco.",
	Long: "Deploy/Update an application to Loco.\n" +
		"This builds and pushes a Docker image to the Loco registry and deploys it onto the Loco platform under the specified subdomain.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return deployCmdFunc(cmd)
	},
}

func init() {
	deployCmd.Flags().StringP("config", "c", "", "path to loco.toml config file")
	deployCmd.Flags().String("org", "", "organization ID")
	deployCmd.Flags().String("workspace", "", "workspace ID")
	deployCmd.Flags().StringP("image", "i", "", "image tag to use for deployment")
	deployCmd.Flags().String("host", "", "Set the host URL")
	deployCmd.Flags().BoolP("wait", "", false, "Wait for the rollout to complete")
}

func deployCmdFunc(cmd *cobra.Command) error {
	ctx := context.Background()

	host, err := getHost(cmd)
	if err != nil {
		return err
	}

	orgID, err := getOrgId(cmd)
	if err != nil {
		return err
	}

	workspaceID, err := getWorkspaceId(cmd)
	if err != nil {
		return err
	}

	configPath, err := parseLocoTomlPath(cmd)
	if err != nil {
		return err
	}

	imageID, err := parseImageId(cmd)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	wait, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrFlagParsing, err)
	}

	locoToken, err := getLocoToken()
	if err != nil {
		return ErrLoginRequired
	}

	loadedCfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if validateErr := config.Validate(loadedCfg.Config); validateErr != nil {
		return fmt.Errorf("%w: %w", ErrValidation, validateErr)
	}
	config.FillSensibleDefaults(loadedCfg.Config)

	cfgValid := lipgloss.NewStyle().
		Render("Validated loco.toml. Beginning deployment!")

	fmt.Println(cfgValid)

	dockerClient, err := docker.NewClient(loadedCfg)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDockerClient, err)
	}
	defer func() {
		if closeErr := dockerClient.Close(); closeErr != nil {
			slog.Debug("failed to close docker client", "error", closeErr)
		}
	}()

	apiClient := client.NewClient(host, locoToken.Token)

	httpClient := shared.NewHTTPClient()
	resourceClient := resourcev1connect.NewResourceServiceClient(httpClient, host)
	registryClient := registryv1connect.NewRegistryServiceClient(httpClient, host)
	domainClient := domainv1connect.NewDomainServiceClient(httpClient, host)

	var resourceID int64

	getAppByNameReq := connect.NewRequest(&resourcev1.GetResourceByNameRequest{
		WorkspaceId: workspaceID,
		Name:        loadedCfg.Config.Metadata.Name,
	})
	getAppByNameReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

	getAppByNameResp, err := resourceClient.GetResourceByName(ctx, getAppByNameReq)
	if err != nil {
		if connect.CodeOf(err) != connect.CodeNotFound {
			logRequestID(ctx, err, "get app by name")
			return fmt.Errorf("failed to get app '%s': %w", loadedCfg.Config.Metadata.Name, err)
		}
	} else {
		resourceID = getAppByNameResp.Msg.Resource.Id
		slog.Debug("found existing app", "app_id", resourceID, "name", getAppByNameResp.Msg.Resource.Name)
	}

	if resourceID == 0 {
		slog.Info("no existing app found, need to create a new one.")

		// Fetch active platform domains
		listDomainsReq := connect.NewRequest(&domainv1.ListActivePlatformDomainsRequest{})
		listDomainsReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

		listDomainsResp, err := domainClient.ListActivePlatformDomains(ctx, listDomainsReq)
		if err != nil {
			logRequestID(ctx, err, "list platform domains")
			return fmt.Errorf("failed to fetch platform domains: %w", err)
		}

		if len(listDomainsResp.Msg.PlatformDomains) == 0 {
			return errors.New("no available platform domains found")
		}

		// Create selection options
		options := make([]ui.SelectOption, len(listDomainsResp.Msg.PlatformDomains))
		for i, domain := range listDomainsResp.Msg.PlatformDomains {
			options[i] = ui.SelectOption{
				Label:       domain.Domain,
				Description: fmt.Sprintf("ID: %d", domain.Id),
				Value:       domain.Id,
			}
		}

		// Let user select domain
		selectedDomainID, err := ui.SelectFromList("Select a domain for your app", options)
		if err != nil {
			return fmt.Errorf("domain selection cancelled: %w", err)
		}

		domainID := selectedDomainID.(int64)
		domainInput := &domainv1.DomainInput{
			DomainSource:     domainv1.DomainType_PLATFORM_PROVIDED,
			Subdomain:        &loadedCfg.Config.Routing.Subdomain,
			PlatformDomainId: &domainID,
		}

		createResourceReq := connect.NewRequest(&resourcev1.CreateResourceRequest{
			WorkspaceId: workspaceID,
			Name:        loadedCfg.Config.Metadata.Name,
			// todo: add to loco config. we need to grab app type from there.
			Type:   resourcev1.ResourceType_SERVICE,
			Domain: domainInput,
		})

		createResourceReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

		createResourceResp, err := resourceClient.CreateResource(ctx, createResourceReq)
		if err != nil {
			logRequestID(ctx, err, "create resource")
			return fmt.Errorf("failed to create resource: %w", err)
		}

		resourceID = createResourceResp.Msg.Resource.Id
		slog.Debug("created resource", "resource_id", resourceID)
	}

	imageBase := "registry.gitlab.com/locomotive-group/loco-ecr"
	imageName := dockerClient.GenerateImageTag(imageBase, orgID, workspaceID, resourceID)

	dockerClient.ImageName = imageName
	slog.Debug("generated image name for build", "imageBase", imageBase, "imageName", imageName)

	buildStep := ui.Step{
		Title: "Build Docker image",
		Run: func(logf func(string)) error {
			if buildErr := dockerClient.BuildImage(ctx, logf); buildErr != nil {
				return fmt.Errorf("%w: %w", ErrDockerBuild, buildErr)
			}
			return nil
		},
	}

	validateStep := ui.Step{
		Title: "Validate and Tag Docker image",
		Run: func(logf func(string)) error {
			if validateErr := dockerClient.ValidateImage(ctx, imageID, logf); validateErr != nil {
				return fmt.Errorf("%w: %w", ErrDockerValidate, validateErr)
			}
			if tagErr := dockerClient.ImageTag(ctx, imageID); tagErr != nil {
				return fmt.Errorf("failed to tag image: %w", tagErr)
			}
			return nil
		},
	}

	var steps []ui.Step

	if imageID != "" {
		steps = append(steps, validateStep)
	} else {
		steps = append(steps, buildStep)
	}

	steps = append(steps, ui.Step{
		Title: "Push image to registry",
		Run: func(logf func(string)) error {
			tokenReq := connect.NewRequest(&registryv1.GitlabTokenRequest{})
			tokenReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))
			// todo: responsible for checking deploy permissions
			tokenResp, err := registryClient.GitlabToken(ctx, tokenReq)
			if err != nil {
				logRequestID(ctx, err, "gitlab token request")
				return fmt.Errorf("failed to fetch registry credentials: %w", err)
			}

			if imageID != "" {
				if tagErr := dockerClient.ImageTag(ctx, imageID); tagErr != nil {
					return fmt.Errorf("failed to tag image: %w", tagErr)
				}
			}

			if pushErr := dockerClient.PushImage(ctx, logf, tokenResp.Msg.GetUsername(), tokenResp.Msg.GetToken()); pushErr != nil {
				return fmt.Errorf("%w: %w", ErrDockerPush, pushErr)
			}
			return nil
		},
	})

	steps = append(steps, ui.Step{
		Title: "Create revision and deployment",
		Run: func(logf func(string)) error {
			return deployApp(ctx, apiClient, resourceID, dockerClient.ImageName, loadedCfg.Config, locoToken.Token, logf, wait)
		},
	})

	if err := ui.RunSteps(steps); err != nil {
		return err
	}

	successMsg := "\n🎉 Deployment scheduled!"
	if wait {
		successMsg = "\n🎉 App deployed!"
	}

	s := lipgloss.NewStyle().
		Bold(true).
		Foreground(ui.LocoLightGreen).
		Render(successMsg)

	fmt.Println(s)

	s = lipgloss.NewStyle().
		Foreground(ui.LocoOrange).
		Render("\nTip: Keep tabs on your app using `loco status`")
	fmt.Println(s)

	return nil
}

func deployApp(ctx context.Context,
	apiClient *client.Client,
	resourceID int64,
	imageName string,
	cfg *config.AppConfig,
	token string,
	logf func(string),
	wait bool,
) error {
	replicas := cfg.Resources.Replicas.Min

	createDeploymentReq := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId: resourceID,
		Spec: &deploymentv1.DeploymentSpec{
			Image:           &imageName,
			InitialReplicas: &replicas,
			Env:             cfg.Env.Variables,
			Cpu:             &cfg.Resources.CPU,
			Memory:          &cfg.Resources.Memory,
			DockerfilePath:  &cfg.Build.DockerfilePath,
			BuildType:       &cfg.Build.Type,
			HealthCheck: &deploymentv1.HealthCheckConfig{
				Path:               &cfg.Health.Path,
				Interval:           &cfg.Health.Interval,
				Timeout:            &cfg.Health.Timeout,
				FailThreshold:      &cfg.Health.FailThreshold,
				StartupGracePeriod: &cfg.Health.StartupGracePeriod,
			},
			Metrics: &deploymentv1.DeploymentMetricsConfig{
				Enabled: &cfg.Obs.Metrics.Enabled,
				Path:    &cfg.Obs.Metrics.Path,
				Port:    &cfg.Obs.Metrics.Port,
			},
		},
	})
	createDeploymentReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", token))

	deploymentResp, err := apiClient.Deployment.CreateDeployment(ctx, createDeploymentReq)
	if err != nil {
		logf(fmt.Sprintf("Failed to create deployment: %v", err))
		return err
	}

	deploymentID := deploymentResp.Msg.DeploymentId
	logf(fmt.Sprintf("Created deployment with version: %d", deploymentID))

	if wait {
		logf("Waiting for deployment to complete...")
		if err := apiClient.StreamDeployment(ctx, fmt.Sprintf("%d", deploymentID), func(event *deploymentv1.DeploymentEvent) error {
			logf(fmt.Sprintf("[%s] %s", event.Status, event.Message))
			if event.ErrorMessage != nil && *event.ErrorMessage != "" {
				logf(fmt.Sprintf("ERROR: %s", *event.ErrorMessage))
				return errors.New(*event.ErrorMessage)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}
