package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"k8s.io/client-go/kubernetes"

	"github.com/team-loco/loco/agent/pkg/applier"
	"github.com/team-loco/loco/agent/pkg/cluster"
	"github.com/team-loco/loco/agent/pkg/kube"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

const (
	heartbeatInterval           = 30 * time.Second
	clusterQueryTimeout         = 10 * time.Second
	defaultControllerDeployment = "controller-loco-manager"
)

type Config struct {
	ControlPlaneURL      string
	AgentToken           string
	Region               string
	AgentVersion         string
	Namespace            string
	ControllerNamespace  string
	ControllerDeployment string
}

func newConfig() *Config {
	namespace := os.Getenv("LOCO_NAMESPACE")
	return &Config{
		ControlPlaneURL:      getEnvOrDefault("CONTROL_PLANE_URL", "http://localhost:8000"),
		AgentToken:           os.Getenv("AGENT_TOKEN"),
		Region:               getEnvOrDefault("REGION", "us-east-1"),
		AgentVersion:         getEnvOrDefault("AGENT_VERSION", "0.1.0"),
		Namespace:            namespace,
		ControllerNamespace:  getEnvOrDefault("LOCO_CONTROLLER_NAMESPACE", namespace),
		ControllerDeployment: getEnvOrDefault("LOCO_CONTROLLER_DEPLOYMENT", defaultControllerDeployment),
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

	if cfg.Namespace == "" {
		slog.Error("LOCO_NAMESPACE environment variable is required")
		os.Exit(1)
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	slog.Info("starting loco agent",
		"control_plane", cfg.ControlPlaneURL,
		"region", cfg.Region,
		"version", cfg.AgentVersion,
		"namespace", cfg.Namespace,
		"controller_namespace", cfg.ControllerNamespace,
		"controller_deployment", cfg.ControllerDeployment,
	)
	transport := &http.Transport{}
	transport.Protocols = new(http.Protocols)
	if strings.HasPrefix(cfg.ControlPlaneURL, "http://") {
		transport.Protocols.SetUnencryptedHTTP2(true)
	} else {
		transport.Protocols.SetHTTP1(true)
		transport.Protocols.SetHTTP2(true)
	}
	httpClient := &http.Client{Transport: transport}

	client := agentv1connect.NewAgentServiceClient(
		httpClient,
		cfg.ControlPlaneURL,
	)

	restConfig, err := kube.RestConfig()
	if err != nil {
		slog.Error("failed to load kubernetes config", "error", err)
		os.Exit(1)
	}

	kubeApplier, err := applier.New(restConfig, cfg.Namespace)
	if err != nil {
		slog.Error("failed to create kubernetes applier", "error", err)
		os.Exit(1)
	}

	inspectorConfig := *restConfig
	inspectorConfig.Timeout = clusterQueryTimeout
	clientset, err := kubernetes.NewForConfig(&inspectorConfig)
	if err != nil {
		slog.Error("failed to create kubernetes clientset", "error", err)
		os.Exit(1)
	}

	inspector := cluster.NewInspector(clientset, cfg.ControllerNamespace, cfg.ControllerDeployment)
	agent := &Agent{
		cfg:       cfg,
		client:    client,
		applier:   kubeApplier,
		inspector: inspector,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("shutdown signal received")
		cancel()
	}()

	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("agent error", "error", err)
		os.Exit(1)
	}
}

// Agent represents the loco agent that runs in each cluster.
type Agent struct {
	cfg       *Config
	client    agentv1connect.AgentServiceClient
	applier   *applier.Applier
	inspector *cluster.Inspector
	clusterID string
}

// Run starts the agent's main loop.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	errCh := make(chan error, 2)

	go func() {
		errCh <- a.runCommandStream(ctx)
	}()

	go func() {
		errCh <- a.runHeartbeat(ctx)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// register announces the agent to the control plane.
func (a *Agent) register(ctx context.Context) error {
	capacity := a.getCapacity(ctx)
	req := connect.NewRequest(&agentv1.RegisterRequest{
		Region:       a.cfg.Region,
		AgentVersion: a.cfg.AgentVersion,
		Capacity:     capacity,
	})
	req.Header().Set("Authorization", "Bearer "+a.cfg.AgentToken)

	resp, err := a.client.Register(ctx, req)
	if err != nil {
		return fmt.Errorf("register RPC failed: %w", err)
	}

	a.clusterID = resp.Msg.GetClusterId()
	slog.InfoContext(ctx, "registered with control plane", "cluster_id", a.clusterID)
	return nil
}

func (a *Agent) runCommandStream(ctx context.Context) error {
	return reconnectLoop(ctx, "command stream", a.commandStreamLoop)
}

func (a *Agent) runHeartbeat(ctx context.Context) error {
	return reconnectLoop(ctx, "heartbeat stream", a.heartbeatLoop)
}

func reconnectLoop(ctx context.Context, name string, attempt func(context.Context) error) error {
	backoff := reconnectBackoff
	for {
		started := time.Now()
		err := attempt(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		elapsed := time.Since(started)
		if elapsed >= healthyStreamDuration {
			backoff = reconnectBackoff
		}
		delay := backoff.Step()
		slog.ErrorContext(ctx, name+" error, reconnecting", "error", err, "delay", delay)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *Agent) commandStreamLoop(ctx context.Context) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream := a.client.CommandStream(streamCtx)
	stream.RequestHeader().Set("Authorization", "Bearer "+a.cfg.AgentToken)
	defer func() {
		cancel()
		closeStream(ctx, "command stream", stream)
	}()

	if err := stream.Send(nil); err != nil {
		openErr := streamError(stream, err)
		return fmt.Errorf("open command stream: %w", openErr)
	}

	slog.InfoContext(ctx, "command stream connected")

	for {
		cmd, err := stream.Receive()
		if err != nil {
			return fmt.Errorf("receive command: %w", err)
		}

		slog.InfoContext(streamCtx, "received command",
			"command_id", cmd.GetCommandId(),
			"type", cmd.GetType().String(),
			"cluster_id", cmd.GetClusterId(),
		)

		ack := a.processCommand(streamCtx, cmd)

		if err := stream.Send(ack); err != nil {
			sendErr := streamError(stream, err)
			return fmt.Errorf("send ack: %w", sendErr)
		}
	}
}

func closeStream[Req, Res any](ctx context.Context, name string, stream *connect.BidiStreamForClient[Req, Res]) {
	if err := stream.CloseRequest(); err != nil {
		slog.DebugContext(ctx, "close "+name+" request", "error", err)
	}
	if err := stream.CloseResponse(); err != nil {
		slog.DebugContext(ctx, "close "+name+" response", "error", err)
	}
}

func streamError[Req, Res any](stream *connect.BidiStreamForClient[Req, Res], sendErr error) error {
	if !errors.Is(sendErr, io.EOF) {
		return sendErr
	}
	_, recvErr := stream.Receive()
	if recvErr == nil || errors.Is(recvErr, io.EOF) {
		return sendErr
	}
	return recvErr
}

// processCommand handles a single command and returns an ack.
func (a *Agent) processCommand(ctx context.Context, cmd *agentv1.CommandStreamResponse) *agentv1.CommandStreamRequest {
	var err error

	switch cmd.GetType() {
	case agentv1.CommandType_COMMAND_TYPE_DEPLOY:
		err = a.handleDeploy(ctx, cmd)
	case agentv1.CommandType_COMMAND_TYPE_DELETE:
		err = a.handleDelete(ctx, cmd)
	default:
		err = fmt.Errorf("%w: unknown command type %s", applier.ErrInvalidPayload, cmd.GetType())
	}

	if err != nil {
		retry := isRetryable(err)
		slog.ErrorContext(ctx, "command failed",
			"command_id", cmd.GetCommandId(),
			"retry", retry,
			"error", err,
		)
		return &agentv1.CommandStreamRequest{
			CommandId:    cmd.GetCommandId(),
			Success:      false,
			ErrorMessage: err.Error(),
			Retry:        retry,
		}
	}

	slog.InfoContext(ctx, "command succeeded", "command_id", cmd.GetCommandId())
	return &agentv1.CommandStreamRequest{
		CommandId: cmd.GetCommandId(),
		Success:   true,
	}
}

// handleDeploy processes a deploy command.
func (a *Agent) handleDeploy(ctx context.Context, cmd *agentv1.CommandStreamResponse) error {
	deploy := cmd.GetDeploy()
	if deploy == nil {
		return fmt.Errorf("%w: deploy payload is nil", applier.ErrInvalidPayload)
	}

	return a.applier.ApplyFromJSON(ctx, deploy.GetApplicationSpec())
}

// handleDelete processes a delete command.
func (a *Agent) handleDelete(ctx context.Context, cmd *agentv1.CommandStreamResponse) error {
	del := cmd.GetDelete()
	if del == nil {
		return fmt.Errorf("%w: delete payload is nil", applier.ErrInvalidPayload)
	}

	return a.applier.DeleteFromJSON(ctx, del.GetResourceId())
}

func (a *Agent) heartbeatLoop(ctx context.Context) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream := a.client.Heartbeat(streamCtx)
	stream.RequestHeader().Set("Authorization", "Bearer "+a.cfg.AgentToken)

	responses := make(chan *agentv1.HeartbeatResponse)
	recvErr := make(chan error, 1)
	recvDone := make(chan struct{})
	receiving := false
	defer func() {
		cancel()
		if receiving {
			<-recvDone
		}
		closeStream(ctx, "heartbeat stream", stream)
	}()

	if err := a.sendHeartbeat(streamCtx, stream); err != nil {
		return err
	}

	slog.InfoContext(ctx, "heartbeat stream connected")

	receiving = true
	go receiveHeartbeats(streamCtx, stream, responses, recvErr, recvDone)

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			return fmt.Errorf("receive heartbeat response: %w", err)
		case resp := <-responses:
			a.handleDirective(resp)
		case <-ticker.C:
			if err := a.sendHeartbeat(streamCtx, stream); err != nil {
				return err
			}
		}
	}
}

func receiveHeartbeats(
	ctx context.Context,
	stream *connect.BidiStreamForClient[agentv1.HeartbeatRequest, agentv1.HeartbeatResponse],
	responses chan<- *agentv1.HeartbeatResponse,
	recvErr chan<- error,
	done chan<- struct{},
) {
	defer close(done)
	for {
		resp, err := stream.Receive()
		if err != nil {
			recvErr <- err
			return
		}
		select {
		case responses <- resp:
		case <-ctx.Done():
			return
		}
	}
}

func (a *Agent) sendHeartbeat(
	ctx context.Context,
	stream *connect.BidiStreamForClient[agentv1.HeartbeatRequest, agentv1.HeartbeatResponse],
) error {
	capacity := a.getCapacity(ctx)
	health := a.getHealth(ctx)
	req := &agentv1.HeartbeatRequest{
		ClusterId: a.clusterID,
		Capacity:  capacity,
		Health:    health,
	}

	if err := stream.Send(req); err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	return nil
}

func (*Agent) handleDirective(resp *agentv1.HeartbeatResponse) {
	if resp == nil {
		return
	}

	switch d := resp.GetDirective().(type) {
	case *agentv1.HeartbeatResponse_Drain:
		slog.Debug("ignoring DRAIN directive", "timeout_seconds", d.Drain.GetTimeoutSeconds())
	case *agentv1.HeartbeatResponse_ReloadConfig:
		slog.Debug("ignoring RELOAD_CONFIG directive", "config", d.ReloadConfig.GetConfig())
	case *agentv1.HeartbeatResponse_Resync:
		slog.Debug("ignoring RESYNC directive", "resource_ids", d.Resync.GetResourceIds())
	}
}

func (a *Agent) getCapacity(ctx context.Context) *agentv1.AgentCapacity {
	capacity, err := a.inspector.Capacity(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to read cluster capacity", "error", err)
		return nil
	}
	return capacity
}

func (a *Agent) getHealth(ctx context.Context) *agentv1.AgentHealth {
	health := a.inspector.Health(ctx)
	if !health.GetKubernetesHealthy() || !health.GetControllerHealthy() {
		slog.WarnContext(ctx, "cluster unhealthy", "message", health.GetMessage())
	}
	return health
}
