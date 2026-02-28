package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/agent/pkg/applier"
	agentv1 "github.com/team-loco/loco/proto/loco/agent/v1"
	"github.com/team-loco/loco/proto/loco/agent/v1/agentv1connect"
)

type Config struct {
	ControlPlaneURL string // e.g., "https://api.loco.dev"
	AgentToken      string // Bearer token for authentication
	Region          string // Region this agent is in
	AgentVersion    string // Version of the agent
}

func newConfig() *Config {
	return &Config{
		ControlPlaneURL: getEnvOrDefault("CONTROL_PLANE_URL", "http://localhost:8000"),
		AgentToken:      os.Getenv("AGENT_TOKEN"),
		Region:          getEnvOrDefault("REGION", "us-east-1"),
		AgentVersion:    getEnvOrDefault("AGENT_VERSION", "0.1.0"),
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func main() {
	cfg := newConfig()

	if cfg.AgentToken == "" {
		slog.Error("AGENT_TOKEN environment variable is required")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting loco agent",
		"control_plane", cfg.ControlPlaneURL,
		"region", cfg.Region,
		"version", cfg.AgentVersion,
	)

	// Create HTTP client
	httpClient := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
		},
	}

	// Create the agent service client
	client := agentv1connect.NewAgentServiceClient(
		httpClient,
		cfg.ControlPlaneURL,
	)

	// Create the Kubernetes applier
	kubeApplier, err := applier.New()
	if err != nil {
		slog.Error("failed to create kubernetes applier", "error", err)
		os.Exit(1)
	}

	agent := &Agent{
		cfg:       cfg,
		client:    client,
		applier:   kubeApplier,
		clusterID: 0, // Will be set after registration
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("shutdown signal received")
		cancel()
	}()

	// Run the agent
	if err := agent.Run(ctx); err != nil {
		slog.Error("agent error", "error", err)
		os.Exit(1)
	}
}

// Agent represents the loco agent that runs in each cluster.
type Agent struct {
	cfg       *Config
	client    agentv1connect.AgentServiceClient
	applier   *applier.Applier
	clusterID int64
}

// Run starts the agent's main loop.
func (a *Agent) Run(ctx context.Context) error {
	// Register with control plane
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	// Start command stream and heartbeat in parallel
	errCh := make(chan error, 2)

	go func() {
		errCh <- a.runCommandStream(ctx)
	}()

	go func() {
		errCh <- a.runHeartbeat(ctx)
	}()

	// Wait for either to fail or context to be cancelled
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// register announces the agent to the control plane.
func (a *Agent) register(ctx context.Context) error {
	req := connect.NewRequest(&agentv1.RegisterRequest{
		Region:       a.cfg.Region,
		AgentVersion: a.cfg.AgentVersion,
		Capacity:     a.getCapacity(),
	})
	req.Header().Set("Authorization", "Bearer "+a.cfg.AgentToken)

	resp, err := a.client.Register(ctx, req)
	if err != nil {
		return fmt.Errorf("register RPC failed: %w", err)
	}

	a.clusterID = resp.Msg.GetClusterId()
	slog.Info("registered with control plane", "cluster_id", a.clusterID)
	return nil
}

// runCommandStream handles the bidirectional command stream.
func (a *Agent) runCommandStream(ctx context.Context) error {
	for {
		if err := a.commandStreamLoop(ctx); err != nil {
			slog.Error("command stream error, reconnecting...", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}
	}
}

func (a *Agent) commandStreamLoop(ctx context.Context) error {
	stream := a.client.CommandStream(ctx)
	stream.RequestHeader().Set("Authorization", "Bearer "+a.cfg.AgentToken)

	slog.Info("command stream connected")

	for {
		cmd, err := stream.Receive()
		if err != nil {
			return fmt.Errorf("receive command: %w", err)
		}

		slog.Info("received command",
			"command_id", cmd.GetCommandId(),
			"type", cmd.GetType().String(),
			"cluster_id", cmd.GetClusterId(),
		)

		// Process the command
		ack := a.processCommand(ctx, cmd)

		// Send ack back
		if err := stream.Send(ack); err != nil {
			return fmt.Errorf("send ack: %w", err)
		}
	}
}

// processCommand handles a single command and returns an ack.
func (a *Agent) processCommand(ctx context.Context, cmd *agentv1.CommandStreamResponse) *agentv1.CommandStreamRequest {
	var err error

	switch cmd.GetType() {
	case agentv1.CommandType_COMMAND_TYPE_DEPLOY:
		err = a.handleDeploy(ctx, cmd)
	case agentv1.CommandType_COMMAND_TYPE_DELETE:
		err = a.handleDelete(ctx, cmd)
	case agentv1.CommandType_COMMAND_TYPE_SCALE:
		err = a.handleScale(ctx, cmd)
	case agentv1.CommandType_COMMAND_TYPE_UPDATE_ENV:
		err = a.handleUpdateEnv(ctx, cmd)
	default:
		err = fmt.Errorf("unknown command type: %s", cmd.GetType())
	}

	if err != nil {
		slog.Error("command failed", "command_id", cmd.GetCommandId(), "error", err)
		return &agentv1.CommandStreamRequest{
			CommandId:    cmd.GetCommandId(),
			Success:      false,
			ErrorMessage: err.Error(),
			Retry:        true, // Let control plane decide on retry
		}
	}

	slog.Info("command succeeded", "command_id", cmd.GetCommandId())
	return &agentv1.CommandStreamRequest{
		CommandId: cmd.GetCommandId(),
		Success:   true,
	}
}

// handleDeploy processes a deploy command.
func (a *Agent) handleDeploy(ctx context.Context, cmd *agentv1.CommandStreamResponse) error {
	deploy := cmd.GetDeploy()
	if deploy == nil {
		return fmt.Errorf("deploy payload is nil")
	}

	return a.applier.ApplyFromJSON(ctx, deploy.GetApplicationSpec())
}

// handleDelete processes a delete command.
func (a *Agent) handleDelete(ctx context.Context, cmd *agentv1.CommandStreamResponse) error {
	del := cmd.GetDelete()
	if del == nil {
		return fmt.Errorf("delete payload is nil")
	}

	return a.applier.DeleteFromJSON(ctx, del.GetResourceId(), del.GetNamespace())
}

// handleScale processes a scale command.
func (a *Agent) handleScale(ctx context.Context, cmd *agentv1.CommandStreamResponse) error {
	// TODO: implement scaling
	return fmt.Errorf("scale command not yet implemented")
}

// handleUpdateEnv processes an update-env command.
func (a *Agent) handleUpdateEnv(ctx context.Context, cmd *agentv1.CommandStreamResponse) error {
	// TODO: implement env update
	return fmt.Errorf("update-env command not yet implemented")
}

// runHeartbeat sends periodic heartbeats to the control plane.
func (a *Agent) runHeartbeat(ctx context.Context) error {
	for {
		if err := a.heartbeatLoop(ctx); err != nil {
			slog.Error("heartbeat stream error, reconnecting...", "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context) error {
	stream := a.client.Heartbeat(ctx)
	stream.RequestHeader().Set("Authorization", "Bearer "+a.cfg.AgentToken)

	slog.Info("heartbeat stream connected")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Send initial heartbeat
	if err := a.sendHeartbeat(stream); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := a.sendHeartbeat(stream); err != nil {
				return err
			}

			// Check for directives (non-blocking receive with timeout)
			// The server may not always send a response, so we use a timeout
			recvCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			resp, err := stream.Receive()
			cancel()
			if err != nil {
				// Timeout or error - continue heartbeat loop
				if recvCtx.Err() == context.DeadlineExceeded {
					continue
				}
				return fmt.Errorf("receive heartbeat response: %w", err)
			}

			// Handle directives based on the oneof
			a.handleDirective(resp)
		}
	}
}

func (a *Agent) sendHeartbeat(stream *connect.BidiStreamForClient[agentv1.HeartbeatRequest, agentv1.HeartbeatResponse]) error {
	req := &agentv1.HeartbeatRequest{
		ClusterId: a.clusterID,
		Capacity:  a.getCapacity(),
		Health:    a.getHealth(),
	}

	if err := stream.Send(req); err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	return nil
}

func (a *Agent) handleDirective(resp *agentv1.HeartbeatResponse) {
	if resp == nil {
		return
	}

	switch d := resp.GetDirective().(type) {
	case *agentv1.HeartbeatResponse_Drain:
		slog.Warn("received DRAIN directive", "timeout_seconds", d.Drain.GetTimeoutSeconds())
		// TODO: implement drain logic
	case *agentv1.HeartbeatResponse_ReloadConfig:
		slog.Warn("received RELOAD_CONFIG directive", "config", d.ReloadConfig.GetConfig())
		// TODO: implement config reload
	case *agentv1.HeartbeatResponse_Resync:
		slog.Warn("received RESYNC directive", "resource_ids", d.Resync.GetResourceIds())
		// TODO: implement resync logic
	}
}

// getCapacity returns the cluster's current capacity.
func (a *Agent) getCapacity() *agentv1.AgentCapacity {
	// TODO: query actual cluster capacity from Kubernetes
	return &agentv1.AgentCapacity{
		CpuMillicoresTotal: 8000,                    // 8 cores
		CpuMillicoresUsed:  4000,                    // 4 cores used
		MemoryBytesTotal:   16 * 1024 * 1024 * 1024, // 16GB
		MemoryBytesUsed:    8 * 1024 * 1024 * 1024,  // 8GB used
		PodsTotal:          110,                     // typical node limit
		PodsRunning:        50,                      // 50 pods running
	}
}

// getHealth returns the agent's health status.
func (a *Agent) getHealth() *agentv1.AgentHealth {
	// TODO: implement actual health checks
	return &agentv1.AgentHealth{
		KubernetesHealthy: true,
		ControllerHealthy: true,
	}
}
