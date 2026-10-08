package resource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

	"connectrpc.com/connect"
	"github.com/joho/godotenv"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/ui"
)

const (
	buildTypeImage      = "image"
	buildTypeDockerfile = "dockerfile"
)

var errNoPinnedImage = errors.New("the API did not return the image it pinned")

type deploymentRequest struct {
	resourceID    string
	environmentID string
	source        *deploymentv1.BuildSource
	cfg           *config.LocoConfig
	wait          bool
}

func imageSource(image string) *deploymentv1.BuildSource {
	return &deploymentv1.BuildSource{Type: buildTypeImage, Image: image}
}

func dockerfileSource(buildID string) *deploymentv1.BuildSource {
	return &deploymentv1.BuildSource{Type: buildTypeDockerfile, BuildId: &buildID}
}

func deploymentRegions(cfg *config.LocoConfig) []string {
	names := maps.Keys(cfg.RegionConfig)
	regions := slices.Sorted(names)
	primary := cfg.Metadata.Region
	if i := slices.Index(regions, primary); i > 0 {
		others := slices.Delete(regions, i, i+1)
		regions = append([]string{primary}, others...)
	}
	return regions
}

func loadDeploymentEnv(cfg *config.LocoConfig) (map[string]string, error) {
	env := make(map[string]string)
	if cfg.Env.File != "" {
		f, openErr := os.Open(cfg.Env.File)
		if openErr != nil {
			return nil, fmt.Errorf("failed to open env file %s: %w", cfg.Env.File, openErr)
		}
		defer f.Close()
		parsed, parseErr := godotenv.Parse(f)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse env file %s: %w", cfg.Env.File, parseErr)
		}
		maps.Copy(env, parsed)
	}
	if cfg.Env.Variables != nil {
		maps.Copy(env, cfg.Env.Variables)
	}
	return env, nil
}

func createDeployments(
	ctx context.Context,
	deploymentClient deploymentv1connect.DeploymentServiceClient,
	authHeader string,
	req deploymentRequest,
	interactive bool,
	out io.Writer,
) error {
	env, err := loadDeploymentEnv(req.cfg)
	if err != nil {
		return err
	}
	regions := deploymentRegions(req.cfg)
	steps := make([]ui.Step, 0, len(regions))
	for _, region := range regions {
		steps = append(steps, ui.Step{
			Title: "Deploy to " + region,
			Run: func(logf func(string)) error {
				created, err := deployRegion(ctx, deploymentClient, authHeader, req, region, env, logf)
				if err != nil {
					return err
				}
				if pinned := created.GetBuild(); pinned.GetType() == buildTypeImage {
					req.source = pinned
				}
				if !req.wait {
					return nil
				}
				return waitForDeployment(ctx, deploymentClient, authHeader, created.GetDeploymentId(), logf)
			},
		})
	}
	return runSteps(steps, interactive, out)
}

func runSteps(steps []ui.Step, interactive bool, out io.Writer) error {
	if interactive {
		return ui.RunSteps(steps)
	}
	for _, step := range steps {
		fmt.Fprintln(out, step.Title)
		logf := func(line string) {
			fmt.Fprintf(out, "  %s\n", line)
		}
		if err := step.Run(logf); err != nil {
			return err
		}
	}
	return nil
}

func serviceDeploymentSpec(
	req deploymentRequest,
	region string,
	env map[string]string,
) *deploymentv1.DeploymentSpec {
	cfg := req.cfg
	healthCheck := &deploymentv1.HealthCheckConfig{
		Path:                cfg.Health.Path,
		InitialDelaySeconds: cfg.Health.StartupGracePeriod,
		IntervalSeconds:     cfg.Health.Interval,
		TimeoutSeconds:      cfg.Health.Timeout,
		FailureThreshold:    cfg.Health.FailThreshold,
	}

	resources := cfg.RegionConfig[region]
	var scalers *deploymentv1.Scalers
	if resources.EnableAutoScaling {
		scalers = &deploymentv1.Scalers{
			Enabled:      true,
			CpuTarget:    &resources.CPUTarget,
			MemoryTarget: &resources.ScalersMemTarget,
		}
	}

	service := &deploymentv1.ServiceDeploymentSpec{
		Build:       req.source,
		HealthCheck: healthCheck,
		Port:        cfg.Routing.Port,
		Cpu:         &resources.CPU,
		Memory:      &resources.Memory,
		MinReplicas: &resources.ReplicasMin,
		MaxReplicas: &resources.ReplicasMax,
		Scalers:     scalers,
		Env:         env,
	}
	return &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}}
}

func deployRegion(
	ctx context.Context,
	deploymentClient deploymentv1connect.DeploymentServiceClient,
	authHeader string,
	req deploymentRequest,
	region string,
	env map[string]string,
	logf func(string),
) (*deploymentv1.CreateDeploymentResponse, error) {
	spec := serviceDeploymentSpec(req, region, env)
	createReq := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId:    req.resourceID,
		Region:        region,
		EnvironmentId: req.environmentID,
		Spec:          spec,
	})
	createReq.Header().Set("Authorization", authHeader)

	resp, err := deploymentClient.CreateDeployment(ctx, createReq)
	if err != nil {
		return nil, fmt.Errorf("create the deployment in %s: %w", region, err)
	}
	created := resp.Msg
	if created.GetBuild().GetImage() == "" {
		return nil, fmt.Errorf("%w: deployment %s in %s", errNoPinnedImage, created.GetDeploymentId(), region)
	}
	logf("Created deployment " + created.GetDeploymentId() + " running " + created.GetBuild().GetImage())
	return created, nil
}

func waitForDeployment(
	ctx context.Context,
	deploymentClient deploymentv1connect.DeploymentServiceClient,
	authHeader string,
	deploymentID string,
	logf func(string),
) error {
	logf("Waiting for the deployment to run...")
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchReq := connect.NewRequest(&deploymentv1.WatchDeploymentRequest{DeploymentId: deploymentID})
	watchReq.Header().Set("Authorization", authHeader)
	stream, err := deploymentClient.WatchDeployment(watchCtx, watchReq)
	if err != nil {
		return fmt.Errorf("watch deployment %s: %w", deploymentID, err)
	}
	defer stream.Close()

	for stream.Receive() {
		event := stream.Msg()
		status := event.GetStatus()
		message := event.GetMessage()
		phase := deploymentPhaseLabel(status)
		line := fmt.Sprintf("[%s] %s", phase, message)
		logf(line)
		switch status {
		case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING:
			return nil
		case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_SUCCEEDED:
			return nil
		case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED:
			return deploymentEndedError(deploymentID, "failed", message)
		case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_CANCELED:
			return deploymentEndedError(deploymentID, "was canceled", message)
		default:
		}
	}
	if streamErr := stream.Err(); streamErr != nil {
		return fmt.Errorf("deployment stream error: %w", streamErr)
	}
	return fmt.Errorf("the deployment %s stream ended before the deployment ran", deploymentID)
}

func deploymentEndedError(deploymentID, outcome, message string) error {
	if message == "" {
		return fmt.Errorf("deployment %s %s", deploymentID, outcome)
	}
	return fmt.Errorf("deployment %s %s: %s", deploymentID, outcome, message)
}

func deploymentPhaseLabel(phase deploymentv1.DeploymentPhase) string {
	switch phase {
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING:
		return "pending"
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING:
		return "deploying"
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING:
		return "running"
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_SUCCEEDED:
		return "succeeded"
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED:
		return "failed"
	case deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_CANCELED:
		return "canceled"
	default:
		return "unknown"
	}
}
