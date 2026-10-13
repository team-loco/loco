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
	"sync"
	"syscall"
	"time"

	"github.com/team-loco/loco/internal/rpc"

	"k8s.io/client-go/kubernetes"

	"github.com/team-loco/loco/agent/pkg/applier"
	"github.com/team-loco/loco/agent/pkg/appwatch"
	"github.com/team-loco/loco/agent/pkg/buildwatch"
	"github.com/team-loco/loco/agent/pkg/cluster"
	"github.com/team-loco/loco/agent/pkg/kube"
	"github.com/team-loco/loco/agent/pkg/reconciler"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

var (
	errBuildSupportChanged      = errors.New("the cluster's build support changed; restarting to pick it up")
	errInvalidHeartbeatInterval = errors.New("control plane sent no positive heartbeat interval")
)

func main() {
	cfg := newAgentConfig()

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	slog.Info("starting loco agent",
		"control_plane", cfg.ControlPlaneURL,
		"version", cfg.AgentVersion,
		"namespace", cfg.Namespace,
		"controller_namespace", cfg.ControllerNamespace,
		"controller_deployment", cfg.ControllerDeployment,
		"build_namespace", cfg.BuildNamespace,
		"observability_proxy_url", cfg.ObservabilityProxyURL,
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

	clients := rpc.NewHTTPClients(httpClient, cfg.ControlPlaneURL, cfg.AgentToken)
	agentClient := clients.Agent

	restConfig, err := kube.RestConfig(cfg.Kubeconfig)
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
	inspectorConfig.Timeout = cfg.ClusterQueryTimeout
	clientset, err := kubernetes.NewForConfig(&inspectorConfig)
	if err != nil {
		slog.Error("failed to create kubernetes clientset", "error", err)
		os.Exit(1)
	}

	inspector := cluster.NewInspector(clientset, cfg.ControllerNamespace, cfg.ControllerDeployment)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := appwatch.Start(ctx, restConfig, cfg.Namespace)
	if err != nil {
		slog.Error("failed to watch Applications", "error", err)
		os.Exit(1)
	}

	buildsEnabled, err := inspector.BuildsEnabled(ctx)
	if err != nil {
		slog.Error("failed to check whether the cluster runs builds", "error", err)
		os.Exit(1)
	}
	buildConfig := buildwatch.Config{
		Namespace:           cfg.BuildNamespace,
		Retention:           cfg.BuildRetention,
		CreateRetryDelay:    cfg.BuildCreateRetryDelay,
		CreateRetryAttempts: cfg.BuildCreateRetryAttempts,
	}
	startWatcher := func() (buildRunner, error) {
		return buildwatch.Start(ctx, restConfig, buildConfig)
	}
	builds, err := startBuilds(buildsEnabled, startWatcher)
	if err != nil {
		slog.Error("failed to watch Builds", "error", err)
		os.Exit(1)
	}
	slog.Info("build support detected", "builds_enabled", buildsEnabled)
	go builds.RunCollector(ctx, cfg.BuildCollectInterval)

	agent := &Agent{
		cfg:           cfg,
		client:        agentClient,
		applier:       kubeApplier,
		inspector:     inspector,
		watcher:       watcher,
		builds:        builds,
		buildsEnabled: buildsEnabled,
	}

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

type buildRunner interface {
	Attach(sink buildwatch.Sink) func()
	WithInventory(ctx context.Context, fn func([]*agentv1.InventoryBuild) error) error
	Handle(ctx context.Context, msg *agentv1.SyncResponse, report buildwatch.Sink)
	RunCollector(ctx context.Context, interval time.Duration)
}

type buildsDetector interface {
	BuildsEnabled(ctx context.Context) (bool, error)
}

func startBuilds(enabled bool, start func() (buildRunner, error)) (buildRunner, error) {
	if !enabled {
		return buildwatch.Disabled{}, nil
	}
	return start()
}

// Agent represents the loco agent that runs in each cluster.
type Agent struct {
	cfg               *Config
	client            agentv1connect.AgentServiceClient
	applier           *applier.Applier
	inspector         *cluster.Inspector
	watcher           *appwatch.Watcher
	builds            buildRunner
	buildsEnabled     bool
	clusterID         string
	heartbeatInterval time.Duration
}

// Run starts the agent's main loop.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	errCh := make(chan error, 2)

	go func() {
		errCh <- a.runSync(ctx)
	}()

	go func() {
		errCh <- watchBuildSupport(ctx, a.inspector, a.buildsEnabled, a.heartbeatInterval)
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
	req := &agentv1.RegisterRequest{
		AgentVersion:          a.cfg.AgentVersion,
		Capacity:              capacity,
		BuildsEnabled:         a.buildsEnabled,
		ObservabilityProxyUrl: a.cfg.ObservabilityProxyURL,
	}

	resp, err := a.client.Register(ctx, req)
	if err != nil {
		return fmt.Errorf("register RPC failed: %w", err)
	}

	heartbeatInterval := resp.GetHeartbeatInterval().AsDuration()
	if heartbeatInterval <= 0 {
		return fmt.Errorf("%w: %s", errInvalidHeartbeatInterval, heartbeatInterval)
	}
	a.clusterID = resp.GetClusterId()
	a.heartbeatInterval = heartbeatInterval
	slog.InfoContext(ctx, "registered with control plane",
		"cluster_id", a.clusterID,
		"heartbeat_interval", a.heartbeatInterval,
	)
	return nil
}

func (a *Agent) runSync(ctx context.Context) error {
	return a.reconnectLoop(ctx, "sync stream", a.syncLoop)
}

func (a *Agent) reconnectLoop(ctx context.Context, name string, attempt func(context.Context) error) error {
	backoff := newReconnectBackoff(a.cfg)
	for {
		started := time.Now()
		err := attempt(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		elapsed := time.Since(started)
		if elapsed >= a.cfg.HealthyStreamDuration {
			backoff = newReconnectBackoff(a.cfg)
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

type syncSession struct {
	stream   agentv1connect.AgentServiceSyncClientStream
	outbound chan *agentv1.SyncRequest
	done     <-chan struct{}
}

func (ss *syncSession) enqueue(msg *agentv1.SyncRequest) {
	select {
	case ss.outbound <- msg:
	case <-ss.done:
	}
}

func (ss *syncSession) sendLoop(ctx context.Context, sendErr chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ss.outbound:
			if err := ss.stream.Send(msg); err != nil {
				sendErr <- streamError(ss.stream, err)
				return
			}
		}
	}
}

func (a *Agent) syncLoop(ctx context.Context) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := a.client.Sync(streamCtx)
	if err != nil {
		return err
	}

	session := &syncSession{
		stream:   stream,
		outbound: make(chan *agentv1.SyncRequest, a.cfg.SyncOutboundBuffer),
		done:     streamCtx.Done(),
	}
	reconcilerConfig := reconciler.Config{
		Workers:        a.cfg.ReconcileWorkers,
		RetryBaseDelay: a.cfg.ReconcileRetryBaseDelay,
		RetryMaxDelay:  a.cfg.ReconcileRetryMaxDelay,
	}
	rec := reconciler.New(a.applier, func(applied *agentv1.Applied) {
		session.enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Applied{Applied: applied}})
	}, reconcilerConfig)

	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
		closeStream(ctx, "sync stream", stream)
	}()

	err = a.sendInventory(streamCtx, func(inventory *agentv1.Inventory) error {
		request := inventoryRequest(inventory)
		if sendErr := stream.Send(request); sendErr != nil {
			openErr := streamError(stream, sendErr)
			return fmt.Errorf("open sync stream: %w", openErr)
		}
		slog.InfoContext(ctx, "sync stream connected",
			"inventory", len(inventory.GetEntries()),
			"builds", len(inventory.GetBuilds()),
		)
		return nil
	})
	if err != nil {
		return err
	}

	buildOps := make(chan *agentv1.SyncResponse, a.cfg.BuildQueueSize)
	sendErr := make(chan error, 1)
	workers.Go(func() { rec.Run(streamCtx) })
	workers.Go(func() { session.sendLoop(streamCtx, sendErr) })
	workers.Go(func() { a.reportInventory(streamCtx, session) })
	workers.Go(func() { a.sendHeartbeats(streamCtx, session.enqueue) })
	workers.Go(func() { a.runBuildOps(streamCtx, buildOps, session.enqueue) })
	detach := a.watcher.Attach(func(status *agentv1.PlacementStatus) {
		session.enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Status{Status: status}})
	})
	defer detach()
	detachBuilds := a.builds.Attach(func(status *agentv1.BuildStatus) {
		session.enqueue(buildStatusRequest(status))
	})
	defer detachBuilds()

	received := make(chan *agentv1.SyncResponse)
	recvErr := make(chan error, 1)
	workers.Go(func() { receiveSync(streamCtx, stream, received, recvErr) })

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sendErr:
			return fmt.Errorf("send sync message: %w", err)
		case err := <-recvErr:
			return fmt.Errorf("receive sync message: %w", err)
		case msg := <-received:
			if isBuildMessage(msg) {
				select {
				case buildOps <- msg:
				case <-streamCtx.Done():
					return streamCtx.Err()
				}
				continue
			}
			submitPlacement(streamCtx, rec, msg)
		}
	}
}

func receiveSync(
	ctx context.Context,
	stream agentv1connect.AgentServiceSyncClientStream,
	received chan<- *agentv1.SyncResponse,
	recvErr chan<- error,
) {
	for {
		msg, err := stream.Receive()
		if err != nil {
			recvErr <- err
			return
		}
		select {
		case received <- msg:
		case <-ctx.Done():
			return
		}
	}
}

func (a *Agent) reportInventory(ctx context.Context, session *syncSession) {
	ticker := time.NewTicker(a.cfg.InventoryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := a.sendInventory(ctx, func(inventory *agentv1.Inventory) error {
				request := inventoryRequest(inventory)
				session.enqueue(request)
				return nil
			})
			if err != nil {
				slog.WarnContext(ctx, "failed to build inventory", "error", err)
			}
		}
	}
}

func (a *Agent) sendInventory(ctx context.Context, send func(*agentv1.Inventory) error) error {
	if err := a.applier.SweepEnvSecrets(ctx); err != nil {
		slog.WarnContext(ctx, "failed to sweep orphaned env Secrets", "error", err)
	}
	return a.builds.WithInventory(ctx, func(builds []*agentv1.InventoryBuild) error {
		inventory, err := a.watcher.Inventory(ctx)
		if err != nil {
			return fmt.Errorf("build inventory: %w", err)
		}
		inventory.Builds = builds
		return send(inventory)
	})
}

func buildStatusRequest(status *agentv1.BuildStatus) *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_BuildStatus{BuildStatus: status}}
}

func isBuildMessage(msg *agentv1.SyncResponse) bool {
	switch msg.GetMessage().(type) {
	case *agentv1.SyncResponse_StartBuild:
		return true
	case *agentv1.SyncResponse_CancelBuild:
		return true
	default:
		return false
	}
}

func (a *Agent) runBuildOps(
	ctx context.Context,
	ops <-chan *agentv1.SyncResponse,
	enqueue func(*agentv1.SyncRequest),
) {
	report := func(status *agentv1.BuildStatus) {
		request := buildStatusRequest(status)
		enqueue(request)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ops:
			a.builds.Handle(ctx, msg, report)
		}
	}
}

func inventoryRequest(inventory *agentv1.Inventory) *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Inventory{Inventory: inventory}}
}

func submitPlacement(ctx context.Context, rec *reconciler.Reconciler, msg *agentv1.SyncResponse) {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncResponse_Apply:
		slog.InfoContext(ctx, "received placement",
			"placement_id", m.Apply.GetPlacementId(),
			"revision", m.Apply.GetRevision(),
			"resource_id", m.Apply.GetResourceId(),
		)
		rec.Submit(reconciler.Work{Placement: applier.Placement{
			ID:          m.Apply.GetPlacementId(),
			Revision:    m.Apply.GetRevision(),
			ResourceID:  m.Apply.GetResourceId(),
			Application: m.Apply.GetApplication(),
			EnvSecret:   envSecretOf(m.Apply.GetEnvSecret()),
		}})
	case *agentv1.SyncResponse_Delete:
		slog.InfoContext(ctx, "received placement deletion",
			"placement_id", m.Delete.GetPlacementId(),
			"revision", m.Delete.GetRevision(),
			"resource_id", m.Delete.GetResourceId(),
		)
		rec.Submit(reconciler.Work{
			Placement: applier.Placement{
				ID:         m.Delete.GetPlacementId(),
				Revision:   m.Delete.GetRevision(),
				ResourceID: m.Delete.GetResourceId(),
			},
			Delete: true,
		})
	default:
		slog.WarnContext(ctx, "ignoring empty sync message")
	}
}

func envSecretOf(secret *agentv1.EnvSecret) *applier.EnvSecret {
	if secret == nil {
		return nil
	}
	return &applier.EnvSecret{Revision: secret.GetRevision(), Data: secret.GetData()}
}

type streamCloser interface {
	CloseSend() error
	Close() error
}

type streamReceiver[Res any] interface {
	Receive() (*Res, error)
}

func closeStream(ctx context.Context, name string, stream streamCloser) {
	if err := stream.CloseSend(); err != nil {
		slog.DebugContext(ctx, "close "+name+" request", "error", err)
	}
	if err := stream.Close(); err != nil {
		slog.DebugContext(ctx, "close "+name+" response", "error", err)
	}
}

func streamError[Res any](stream streamReceiver[Res], sendErr error) error {
	if !errors.Is(sendErr, io.EOF) {
		return sendErr
	}
	_, recvErr := stream.Receive()
	if recvErr == nil || errors.Is(recvErr, io.EOF) {
		return sendErr
	}
	return recvErr
}

func (a *Agent) sendHeartbeats(ctx context.Context, enqueue func(*agentv1.SyncRequest)) {
	ticker := time.NewTicker(a.heartbeatInterval)
	defer ticker.Stop()
	for {
		heartbeat := a.heartbeat(ctx)
		enqueue(&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: heartbeat}})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Agent) heartbeat(ctx context.Context) *agentv1.Heartbeat {
	capacity := a.getCapacity(ctx)
	health := a.getHealth(ctx)
	return &agentv1.Heartbeat{
		Capacity:      capacity,
		Health:        health,
		BuildsEnabled: a.buildsEnabled,
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

func watchBuildSupport(
	ctx context.Context,
	detector buildsDetector,
	started bool,
	interval time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			enabled, err := detector.BuildsEnabled(ctx)
			if err != nil {
				slog.WarnContext(ctx, "failed to check whether the cluster runs builds", "error", err)
				continue
			}
			if enabled != started {
				return fmt.Errorf("%w: builds_enabled went from %t to %t", errBuildSupportChanged, started, enabled)
			}
		}
	}
}
