package resource

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"

	"connectrpc.com/connect"
	"github.com/joho/godotenv"
	"github.com/team-loco/loco/internal/config"
	"github.com/team-loco/loco/internal/ui"
	deploymentv1 "github.com/team-loco/loco/proto/loco/deployment/v1"
	"github.com/team-loco/loco/proto/loco/deployment/v1/deploymentv1connect"
)

func createDeployment(
	ctx context.Context,
	deploymentClient deploymentv1connect.DeploymentServiceClient,
	authHeader string,
	resourceID string,
	imageName string,
	cfg *config.LocoConfig,
	wait bool,
) error {
	steps := []ui.Step{
		{
			Title: "Create deployment",
			Run: func(logf func(string)) error {
				return doCreateDeployment(ctx, deploymentClient, authHeader, resourceID, imageName, cfg, logf, wait)
			},
		},
	}

	return ui.RunSteps(steps)
}

func doCreateDeployment(
	ctx context.Context,
	deploymentClient deploymentv1connect.DeploymentServiceClient,
	authHeader string,
	resourceID string,
	imageName string,
	cfg *config.LocoConfig,
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

	createReq := connect.NewRequest(&deploymentv1.CreateDeploymentRequest{
		ResourceId: resourceID,
		Spec:       deploymentSpec,
	})
	createReq.Header().Set("Authorization", authHeader)

	resp, err := deploymentClient.CreateDeployment(ctx, createReq)
	if err != nil {
		logf(fmt.Sprintf("Failed to create deployment: %v", err))
		return err
	}

	deploymentID := resp.Msg.DeploymentId
	logf(fmt.Sprintf("Created deployment with version: %s", deploymentID))

	if wait {
		logf("Waiting for deployment to complete...")
		watchReq := connect.NewRequest(&deploymentv1.WatchDeploymentRequest{
			DeploymentId: deploymentID,
		})
		watchReq.Header().Set("Authorization", authHeader)

		stream, err := deploymentClient.WatchDeployment(ctx, watchReq)
		if err != nil {
			return fmt.Errorf("failed to watch deployment: %w", err)
		}

		for stream.Receive() {
			event := stream.Msg()
			logf(fmt.Sprintf("[%s] %s", event.Status, event.Message))
			if event.Status == deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED && event.Message != "" {
				return errors.New(event.Message)
			}
		}

		if err := stream.Err(); err != nil {
			return fmt.Errorf("deployment stream error: %w", err)
		}
	}

	return nil
}
