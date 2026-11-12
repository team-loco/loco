package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/team-loco/loco/agent/pkg/applier"
	"github.com/team-loco/loco/agent/pkg/appwatch"
	"github.com/team-loco/loco/agent/pkg/buildwatch"
	"github.com/team-loco/loco/agent/pkg/cluster"
	"github.com/team-loco/loco/agent/pkg/domains"
	"github.com/team-loco/loco/agent/pkg/identity"
	"github.com/team-loco/loco/agent/pkg/kube"
	"github.com/team-loco/loco/agent/pkg/registrycred"
	"github.com/team-loco/loco/agent/pkg/telemetrytoken"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
)

// agentLoops is the number of goroutines Agent.Run starts. Each gets a slot in errCh so none
// blocks on its send after Run has returned.
const agentLoops = 5

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
		"registry_auth", cfg.RegistryAuth,
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

	credentials := identity.NewCredentials(cfg.AssertionLifetime)
	rpcTransport := connecthttp.NewTransport(httpClient, cfg.ControlPlaneURL)
	connectClient := connect.NewClient(rpcTransport, credentials.Interceptor())
	agentClient := agentv1connect.NewAgentServiceClient(connectClient)
	ingestClient := ingestv1connect.NewIngestServiceClient(connectClient)
	registryClient := registryv1connect.NewRegistryServiceClient(connectClient)

	restConfig, err := kube.RestConfig(cfg.Kubeconfig)
	if err != nil {
		slog.Error("failed to load kubernetes config", "error", err)
		os.Exit(1)
	}

	var pullSecrets *registrycred.Manager
	var applierPullSecrets applier.PullSecrets
	if cfg.RegistryTokenMode() {
		backoff := func() wait.Backoff { return newReconnectBackoff(cfg) }
		pullSecrets, err = registrycred.New(
			restConfig,
			registryClient,
			cfg.Namespace,
			cfg.RegistryCheckInterval,
			backoff,
		)
		if err != nil {
			slog.Error("failed to create the registry pull credential manager", "error", err)
			os.Exit(1)
		}
		applierPullSecrets = pullSecrets
	}

	kubeApplier, err := applier.New(restConfig, cfg.Namespace, applierPullSecrets)
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
	if err = inspector.CheckKubernetesVersion(); err != nil {
		slog.Error("Kubernetes compatibility check failed", "error", err)
		os.Exit(1)
	}
	domainReader, err := domains.NewReader(&inspectorConfig)
	if err != nil {
		slog.Error("failed to create the gateway client", "error", err)
		os.Exit(1)
	}
	domainReporter := domains.New(domainReader, domains.Config{
		Namespace:   cfg.Namespace,
		Gateway:     cfg.Gateway,
		Certificate: cfg.Certificate,
		Domains:     cfg.AppDomains,
	})
	identities := identity.NewStore(clientset, cfg.Namespace, cfg.KeySecret)

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
		TokenMode:           cfg.RegistryTokenMode(),
	}
	var builds buildRunner = buildwatch.Disabled{}
	if buildsEnabled {
		builds, err = buildwatch.Start(ctx, restConfig, buildConfig)
		if err != nil {
			slog.Error("failed to watch Builds", "error", err)
			os.Exit(1)
		}
	}
	slog.Info("build support detected", "builds_enabled", buildsEnabled)
	go builds.RunCollector(ctx, cfg.BuildCollectInterval)

	var telemetryTokens *telemetrytoken.Renewer
	if cfg.IngestTokenSecret.Name != "" {
		backoff := func() wait.Backoff { return newReconnectBackoff(cfg) }
		telemetryTokens = telemetrytoken.NewRenewer(ingestClient, clientset, cfg.IngestTokenSecret, backoff)
	}

	agent := &Agent{
		cfg:           cfg,
		client:        agentClient,
		applier:       kubeApplier,
		inspector:     inspector,
		domains:       domainReporter,
		identities:    identities,
		credentials:   credentials,
		watcher:       watcher,
		builds:        builds,
		buildsEnabled: buildsEnabled,
		ingest:        telemetryTokens,
		pullSecrets:   pullSecrets,
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
	Wait(ctx context.Context) error
}

// Agent represents the loco agent that runs in each cluster.
type Agent struct {
	cfg               *Config
	client            agentv1connect.AgentServiceClient
	applier           *applier.Applier
	inspector         *cluster.Inspector
	domains           *domains.Reporter
	identities        *identity.Store
	credentials       *identity.Credentials
	watcher           *appwatch.Watcher
	builds            buildRunner
	buildsEnabled     bool
	heartbeatInterval time.Duration
	ingest            *telemetrytoken.Renewer
	pullSecrets       *registrycred.Manager
}

// Run starts the agent's main loop.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	if a.pullSecrets != nil {
		go a.pullSecrets.Run(ctx)
	}
	go a.sweepEnvSecrets(ctx)

	errCh := make(chan error, agentLoops)

	if a.ingest != nil {
		go func() {
			errCh <- a.ingest.Run(ctx)
		}()
	}

	go func() {
		errCh <- a.runSync(ctx)
	}()

	go func() {
		errCh <- watchBuildSupport(ctx, a.inspector, a.buildsEnabled, a.heartbeatInterval)
	}()

	go func() {
		errCh <- a.watcher.Wait(ctx)
	}()

	go func() {
		errCh <- a.builds.Wait(ctx)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
