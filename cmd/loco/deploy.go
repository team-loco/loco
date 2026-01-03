package loco

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"strings"

	"connectrpc.com/connect"
	"github.com/charmbracelet/lipgloss"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/internal/client"
	"github.com/team-loco/loco/internal/docker"
	"github.com/team-loco/loco/internal/ui"
	"github.com/team-loco/loco/shared"
	"github.com/team-loco/loco/shared/config"
	deploymentv1 "github.com/team-loco/loco/shared/proto/deployment/v1"
	domainv1 "github.com/team-loco/loco/shared/proto/domain/v1"
	"github.com/team-loco/loco/shared/proto/domain/v1/domainv1connect"
	registryv1 "github.com/team-loco/loco/shared/proto/registry/v1"
	registryv1connect "github.com/team-loco/loco/shared/proto/registry/v1/registryv1connect"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
	"github.com/team-loco/loco/shared/proto/resource/v1/resourcev1connect"
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

	getAppByNameReq := connect.NewRequest(&resourcev1.GetResourceRequest{
		WorkspaceId: &workspaceID,
		Name:        &loadedCfg.Config.Metadata.Name,
	})
	getAppByNameReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

	getAppByNameResp, err := resourceClient.GetResource(ctx, getAppByNameReq)
	if err != nil {
		if connect.CodeOf(err) != connect.CodeNotFound {
			logRequestID(ctx, err, "get app by name")
			return fmt.Errorf("failed to get app '%s': %w", loadedCfg.Config.Metadata.Name, err)
		}
	} else {
		resourceID = getAppByNameResp.Msg.Id
		slog.Debug("found existing app", "app_id", resourceID, "name", getAppByNameResp.Msg.Name)
	}

	if resourceID == 0 {
		slog.Info("no existing app found, need to create a new one.")

		// Log available regions from config if present
		if len(loadedCfg.Config.RegionConfig) > 0 {
			var regions []string
			for region := range loadedCfg.Config.RegionConfig {
				regions = append(regions, region)
			}
			slog.Info("using regions from config", "regions", regions)
		} else {
			// If no regional config, prompt for at least one region
			listRegionsReq := connect.NewRequest(&resourcev1.ListRegionsRequest{})
			listRegionsReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

			listRegionsResp, regionsErr := resourceClient.ListRegions(ctx, listRegionsReq)
			if regionsErr != nil {
				logRequestID(ctx, regionsErr, "list regions")
				return fmt.Errorf("failed to fetch regions: %w", regionsErr)
			}

			if len(listRegionsResp.Msg.Regions) == 0 {
				return errors.New("no available regions found")
			}

			// Create selection options
			regionOptions := make([]ui.SelectOption, len(listRegionsResp.Msg.Regions))
			for i, r := range listRegionsResp.Msg.Regions {
				label := r.Region
				if r.IsDefault {
					label += " (default)"
				}
				regionOptions[i] = ui.SelectOption{
					Label:       label,
					Description: fmt.Sprintf("Health: %s", r.HealthStatus),
					Value:       r.Region,
				}
			}

			// Let user select region
			selectedRegion, selErr := ui.SelectFromList("Select a region for your app", regionOptions)
			if selErr != nil {
				return fmt.Errorf("region selection cancelled: %w", selErr)
			}

			regionStr, ok := selectedRegion.(string)
			if !ok {
				return fmt.Errorf("invalid region type: expected string, got %T", selectedRegion)
			}
			slog.Info("selected region", "region", regionStr)
		}

		// Extract subdomain from hostname
		subdomain := config.ExtractSubdomainFromHostname(loadedCfg.Config.DomainConfig.Hostname)
		if subdomain == "" {
			return errors.New("failed to extract subdomain from hostname")
		}

		// Determine domain input based on type
		var domainInput *domainv1.DomainInput

		if loadedCfg.Config.DomainConfig.Type == "custom" {
			// Custom domain - use the full hostname as-is
			domainInput = &domainv1.DomainInput{
				DomainSource: domainv1.DomainType_USER_PROVIDED,
				Domain:       &loadedCfg.Config.DomainConfig.Hostname,
			}
			slog.Info("using custom domain from config", "domain", loadedCfg.Config.DomainConfig.Hostname)
		} else {
			// Platform domain - need to resolve the base domain and use subdomain
			activeOnlyVal := true
			listDomainsReq := connect.NewRequest(&domainv1.ListPlatformDomainsRequest{
				ActiveOnly: &activeOnlyVal,
				Limit:      100,
				Offset:     0,
			})
			listDomainsReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

			listDomainsResp, domainsErr := domainClient.ListPlatformDomains(ctx, listDomainsReq)
			if domainsErr != nil {
				logRequestID(ctx, domainsErr, "list platform domains")
				return fmt.Errorf("failed to fetch platform domains: %w", domainsErr)
			}

			if len(listDomainsResp.Msg.PlatformDomains) == 0 {
				return errors.New("no available platform domains found")
			}

			// Find matching platform domain by extracting base from hostname
			// hostname format: "subdomain.base-domain.com" -> need to find "base-domain.com" in available domains
			var foundDomainID int64
			for _, pd := range listDomainsResp.Msg.PlatformDomains {
				if strings.HasSuffix(loadedCfg.Config.DomainConfig.Hostname, pd.Domain) {
					foundDomainID = pd.Id
					slog.Info("matched platform domain", "hostname", loadedCfg.Config.DomainConfig.Hostname, "platform_domain", pd.Domain, "id", pd.Id)
					break
				}
			}

			if foundDomainID == 0 {
				// If exact match not found, show interactive selection
				options := make([]ui.SelectOption, len(listDomainsResp.Msg.PlatformDomains))
				for i, domain := range listDomainsResp.Msg.PlatformDomains {
					options[i] = ui.SelectOption{
						Label:       domain.Domain,
						Description: fmt.Sprintf("ID: %d", domain.Id),
						Value:       domain.Id,
					}
				}

				selectedDomainID, domainSelErr := ui.SelectFromList("Select platform domain for your app", options)
				if domainSelErr != nil {
					return fmt.Errorf("domain selection cancelled: %w", domainSelErr)
				}

				domainID, ok := selectedDomainID.(int64)
				if !ok {
					return fmt.Errorf("invalid domain ID type: expected int64, got %T", selectedDomainID)
				}
				foundDomainID = domainID
			}

			domainInput = &domainv1.DomainInput{
				DomainSource:     domainv1.DomainType_PLATFORM_PROVIDED,
				Subdomain:        &subdomain,
				PlatformDomainId: &foundDomainID,
			}
		}

		// convert config to ResourceSpec (v1 schema)
		resourceSpec, specErr := configToResourceSpec(loadedCfg.Config, "v1")
		if specErr != nil {
			return fmt.Errorf("failed to convert config to resource spec: %w", specErr)
		}

		createResourceReq := connect.NewRequest(&resourcev1.CreateResourceRequest{
			WorkspaceId: workspaceID,
			Name:        loadedCfg.Config.Metadata.Name,
			// todo: add to loco config. we need to grab app type from there.
			Type:   resourcev1.ResourceType_SERVICE,
			Domain: domainInput,
			Spec:   resourceSpec,
		})

		createResourceReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

		createResourceResp, createErr := resourceClient.CreateResource(ctx, createResourceReq)
		if createErr != nil {
			logRequestID(ctx, createErr, "create resource")
			return fmt.Errorf("failed to create resource: %w", createErr)
		}

		resourceID = createResourceResp.Msg.Id
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
			tokenResp, tokenErr := registryClient.GetGitlabToken(ctx, tokenReq)
			if tokenErr != nil {
				logRequestID(ctx, tokenErr, "gitlab token request")
				return fmt.Errorf("failed to fetch registry credentials: %w", tokenErr)
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

	// Fetch resource to verify it exists
	getResourceReq := connect.NewRequest(&resourcev1.GetResourceRequest{
		ResourceId: &resourceID,
	})
	getResourceReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", locoToken.Token))

	_, err = resourceClient.GetResource(ctx, getResourceReq)
	if err != nil {
		return fmt.Errorf("failed to fetch resource: %w", err)
	}

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
	cfg *config.LocoConfig,
	token string,
	logf func(string),
	wait bool,
) error {
	buildSource := &deploymentv1.BuildSource{
		Type:           cfg.Build.Type,
		Image:          imageName,
		DockerfilePath: &cfg.Build.DockerfilePath,
	}

	healthCheck := &deploymentv1.HealthCheckConfig{
		Path:                cfg.Health.Path,
		InitialDelaySeconds: cfg.Health.StartupGracePeriod,
		IntervalSeconds:     cfg.Health.Interval,
		TimeoutSeconds:      cfg.Health.Timeout,
		FailureThreshold:    cfg.Health.FailThreshold,
	}

	// get primary region for resource defaults
	primaryRegion := cfg.RegionConfig[cfg.Metadata.Region]

	var scalers *deploymentv1.Scalers
	if primaryRegion.EnableAutoScaling {
		scalers = &deploymentv1.Scalers{
			Enabled:      true,
			CpuTarget:    &primaryRegion.CPUTarget,
			MemoryTarget: &primaryRegion.ScalersMemTarget,
		}
	}

	env := make(map[string]string)

	if cfg.Env.File != "" {
		f, openErr := os.Open(cfg.Env.File)
		if openErr != nil {
			return fmt.Errorf("failed to open env file %s: %w", cfg.Env.File, openErr)
		}
		defer f.Close()
		parsed, parseErr := godotenv.Parse(f)
		if parseErr != nil {
			return fmt.Errorf("failed to parse env file %s: %w", cfg.Env.File, parseErr)
		}
		maps.Copy(env, parsed)
	}

	if cfg.Env.Variables != nil {
		maps.Copy(env, cfg.Env.Variables)
	}

	serviceDeploymentSpec := &deploymentv1.ServiceDeploymentSpec{
		Build:       buildSource,
		HealthCheck: healthCheck,
		Port:        cfg.Routing.Port,
		Cpu:         &primaryRegion.CPU,
		Memory:      &primaryRegion.Memory,
		MinReplicas: &primaryRegion.ReplicasMin,
		MaxReplicas: &primaryRegion.ReplicasMax,
		Scalers:     scalers,
		Env:         env,
	}

	deploymentSpec := &deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{
			Service: serviceDeploymentSpec,
		},
	}

	createDeploymentReq := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId: resourceID,
		Spec:       deploymentSpec,
	})
	createDeploymentReq.Header().Set("Authorization", fmt.Sprintf("Bearer %s", token))

	deploymentResp, err := apiClient.Deployment.CreateDeployment(ctx, createDeploymentReq)
	if err != nil {
		logf(fmt.Sprintf("Failed to create deployment: %v", err))
		return err
	}

	deploymentID := deploymentResp.Msg.GetId()
	logf(fmt.Sprintf("Created deployment with version: %d", deploymentID))

	if wait {
		logf("Waiting for deployment to complete...")
		if err := apiClient.StreamDeployment(ctx, fmt.Sprintf("%d", deploymentID), func(event *deploymentv1.DeploymentEvent) error {
			logf(fmt.Sprintf("[%s] %s", event.Status, event.Message))
			if event.Status == deploymentv1.DeploymentPhase_FAILED && event.Message != "" {
				logf(fmt.Sprintf("ERROR: %s", event.Message))
				return errors.New(event.Message)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}
