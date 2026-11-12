package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	charmLog "charm.land/log/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/grpcreflect/v2"
	"connectrpc.com/validate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/cors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/supabase"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/migrations"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/pkg/clusternotify"
	"github.com/team-loco/loco/api/pkg/imageresolver"
	"github.com/team-loco/loco/api/pkg/registrytoken"
	"github.com/team-loco/loco/api/pkg/transit"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/sweepers"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/webhooks"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/gen/go/loco/bootstrap/v1/bootstrapv1connect"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1connect "github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	planv1connect "github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/gen/go/loco/webhook/v1/webhookv1connect"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"github.com/team-loco/loco/internal/ingesttoken"
)

const (
	envProduction           = "PRODUCTION"
	eventsRetentionInterval = time.Hour
)

var version string

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func withCORS(allowedOrigins []string, allowLoopback bool) func(http.Handler) http.Handler {
	exposedHeaders := append(connectcors.ExposedHeaders(), "x-loco-request-id")
	opts := cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   connectcors.AllowedMethods(),
		AllowedHeaders:   append(connectcors.AllowedHeaders(), "Authorization"),
		ExposedHeaders:   exposedHeaders,
		AllowCredentials: true,
	}
	if allowLoopback {
		opts.AllowOriginFunc = func(origin string) bool {
			return slices.Contains(allowedOrigins, origin) || isLoopbackOrigin(origin)
		}
	}
	return func(h http.Handler) http.Handler {
		middleware := cors.New(opts)
		return middleware.Handler(h)
	}
}

func newPprofServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func newOutboundHTTPClient() *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	transport := base.Clone()
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.Protocols.SetHTTP2(true)
	traced := otelhttp.NewTransport(transport)
	return &http.Client{Transport: traced, Timeout: 15 * time.Second}
}

func serve(server *http.Server) error {
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("loco api failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ac := newAPIConfig()

	logger := slog.New(CustomHandler{Handler: getLoggerHandler(ac)})
	slog.SetDefault(logger)
	slog.Info("starting loco api", "version", ac.Version)

	tracerProvider, err := startTracing(context.Background(), ac.TracingEnabled, ac.Version)
	if err != nil {
		return err
	}
	defer stopTracing(tracerProvider)

	if err = migrations.Up(context.Background(), ac.Database.URL); err != nil {
		return err
	}
	pool, err := db.NewPool(context.Background(), ac.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	appCache, err := newCache(ac.CacheType, ac.CacheAddr)
	if err != nil {
		return fmt.Errorf("create cache: %w", err)
	}
	defer appCache.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := newDeps(ctx, ac, pool, appCache)
	if err != nil {
		return err
	}
	baseServer, authenticatedServer, err := newRPCServers(ctx, ac, d)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	registerHandlers(mux, baseServer, authenticatedServer, d)
	mountRPCServers(mux, baseServer, authenticatedServer)
	mountOpenAPI(mux, ac.Version)
	mountObservabilityProxy(mux, ac.ObservabilityProxyURL)

	var background sync.WaitGroup
	var pprofServer *http.Server
	defer func() {
		stop()
		if pprofServer != nil {
			// Closing here lets the pprof goroutine finish when run returns before shutdown does.
			if closeErr := pprofServer.Close(); closeErr != nil {
				slog.Warn("failed to close pprof server", "error", closeErr)
			}
		}
		background.Wait()
	}()
	startBackground(ctx, &background, ac, d)

	server := newServer(ac, mux)
	if ac.PprofAddr != "" {
		pprofServer = newPprofServer(ac.PprofAddr)
		background.Go(func() {
			slog.Info("starting pprof server", "addr", pprofServer.Addr)
			if err := pprofServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("pprof server error", "error", err)
			}
		})
	}

	slog.Info("starting server", "addr", server.Addr)
	served := make(chan error, 1)
	go func() { served <- serve(server) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	slog.InfoContext(ctx, "shutdown signal received", "signal", context.Cause(ctx))
	shutdownErr := shutdown(server, pprofServer, ac.ShutdownDrainTimeout)
	return errors.Join(shutdownErr, <-served)
}

type deps struct {
	machine           *tvm.VendingMachine
	queries           *genDb.Queries
	verifier          *auth.Verifier
	resolver          *auth.Resolver
	authorizer        *authz.Authorizer
	webhookDispatcher *webhooks.Dispatcher
	placementNotifier *clusternotify.Notifier
	// The sweepers are nil when the store they sweep is not configured.
	sourceSweeper   *sweepers.SourceSweeper
	imageSweeper    *sweepers.ImageSweeper
	registrySweeper *sweepers.RegistrySweeper

	config       *service.ConfigServer
	clusterAdmin *service.ClusterAdminServer
	bootstrap    *service.BootstrapServer
	user         *service.UserServer
	auth         *service.AuthServer
	event        *service.EventServer
	webhook      *service.WebhookServer
	org          *service.OrgServer
	workspace    *service.WorkspaceServer
	resource     *service.ResourceServer
	deployment   *service.DeploymentServer
	build        *service.BuildServer
	domain       *service.DomainServer
	token        *service.TokenServer
	agent        *service.AgentServer
	ingest       *service.IngestServer
	registry     *service.RegistryServer
	plan         *service.PlanServer
	environment  *service.EnvironmentServer
	secret       *service.SecretServer
	// registryKey is nil unless registry auth runs in token mode.
	registryKey http.HandlerFunc
}

func newDeps(ctx context.Context, ac *APIConfig, pool *pgxpool.Pool, appCache cache.Cache) (*deps, error) {
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, ac.Tokens)

	authClient := newOutboundHTTPClient()
	adminFactories := auth.AdminFactories{supabase.AdminType: supabase.NewAdmin}
	admins, err := auth.NewAdmins(authClient, ac.AuthIssuers, adminFactories)
	if err != nil {
		return nil, fmt.Errorf("AUTH_ISSUERS admin: %w", err)
	}
	emailVerifiers, err := auth.NewEmailVerifiers(ac.AuthIssuers, admins)
	if err != nil {
		return nil, fmt.Errorf("AUTH_ISSUERS email verification: %w", err)
	}
	verifier := auth.NewVerifier(authClient, ac.AuthIssuers)
	emailVerifierOption := auth.WithEmailVerifiers(emailVerifiers)
	resolver := auth.NewResolver(pool, ac.SignupPolicy, ac.CountLimits, emailVerifierOption)

	placementNotifier := clusternotify.New(ac.DatabaseListenConfig, ac.PlacementPollInterval)
	if err = placementNotifier.Connect(ctx); err != nil {
		return nil, fmt.Errorf("start placement listener: %w", err)
	}

	workspaceWebhookClient := webhooks.NewClient(ac.Webhooks, ac.WebhooksAllowPrivate)
	installWebhookClient := webhooks.NewClient(ac.Webhooks, true)
	authServiceHandler := service.NewAuthServer(pool, queries, machine, appCache, admins, ac.WebURL)
	clusterSilentAfter := service.ClusterSilentAfter(ac.HeartbeatInterval)

	sourceBucket, err := newSourceBucket(ac.SourceBucket)
	if err != nil {
		return nil, err
	}
	if ac.RegistryHost == "" {
		slog.Warn("LOCO_REGISTRY_HOST is not set; builds are disabled")
	}
	var sourceSweeper *sweepers.SourceSweeper
	if sourceBucket != nil {
		sourceSweeper = sweepers.NewSourceSweeper(pool, queries, sourceBucket, ac.SourceSweep)
	}
	imageResolver := imageresolver.New(ac.ImageResolveTimeout)

	var registryKey *transit.PinnedKey
	if ac.RegistryAuth.TokenMode() {
		registryKey = &ac.RegistryAuth.TransitKey
	}
	transitKeys, err := newTransitKeys(ctx, ac.Transit, ac.Secrets, ac.Ingest.TransitKey, registryKey)
	if err != nil {
		return nil, fmt.Errorf("verify the transit keys: %w", err)
	}
	secretKeyProvider := transitKeys.Provider
	if err = webhooks.SyncInstallWebhooks(ctx, pool, secretKeyProvider, ac.InstallWebhooks); err != nil {
		return nil, fmt.Errorf("INSTALL_WEBHOOKS: %w", err)
	}
	clusterAuthenticator := service.NewClusterAuthenticator(queries, ac.Agent.AssertionMaxLifetime)
	registryConfig := service.RegistryConfig{
		TokenMode:              ac.RegistryAuth.TokenMode(),
		Service:                ac.RegistryAuth.Service,
		Issuer:                 ac.WebURL,
		RegistryHost:           ac.RegistryHost,
		RegistryPrefix:         ac.RegistryPrefix,
		AccessTokenLifetime:    ac.RegistryAuth.AccessTokenLifetime,
		PullCredentialLifetime: ac.RegistryAuth.PullCredentialLifetime,
	}
	registryServiceHandler := service.NewRegistryServer(
		queries,
		clusterAuthenticator,
		transitKeys.Registry,
		transitKeys.RegistryKeyID,
		registryConfig,
	)
	registryClientConfig := ac.Registry
	if ac.RegistryAuth.TokenMode() {
		registryClientConfig.TokenSource = registryServiceHandler.SignAPIToken
	}
	imageRegistry, err := newImageRegistry(registryClientConfig, ac.ImageSweep.Retention)
	if err != nil {
		return nil, err
	}
	var imageSweeper *sweepers.ImageSweeper
	if imageRegistry != nil {
		imageSweeper = sweepers.NewImageSweeper(pool, queries, imageRegistry, ac.ImageSweep)
	}
	var registryKeyHandler http.HandlerFunc
	var registrySweeper *sweepers.RegistrySweeper
	if registryKey != nil {
		registryKeyHandler, err = service.NewRegistryKeyHandler(transitKeys.RegistryPublicKey)
		if err != nil {
			return nil, fmt.Errorf("encode the registry public key: %w", err)
		}
		registrySweeper = sweepers.NewRegistrySweeper(pool, ac.RegistryAuth.CredentialSweep)
	}

	webhookDispatcher := webhooks.NewDispatcher(
		queries, secretKeyProvider, ac.Webhooks, workspaceWebhookClient, installWebhookClient,
	)
	return &deps{
		machine:           machine,
		queries:           queries,
		verifier:          verifier,
		resolver:          resolver,
		authorizer:        authz.New(pool, queries),
		webhookDispatcher: webhookDispatcher,
		placementNotifier: placementNotifier,
		sourceSweeper:     sourceSweeper,
		imageSweeper:      imageSweeper,
		registrySweeper:   registrySweeper,

		config: service.NewConfigServer(
			ac.MinCLIVersion,
			ac.SchemaURL,
			ac.ServiceDefaults,
			ac.AuthIssuers,
			ac.OrgLinks,
			ac.Tokens.MaxAPITokenDuration,
			ac.CountLimits,
		),
		clusterAdmin: service.NewClusterAdminServer(pool, queries, ac.Agent.JoinTokenTTL),
		bootstrap: service.NewBootstrapServer(
			pool, ac.BootstrapTokenHash, verifier, resolver, authServiceHandler,
		),
		user:  service.NewUserServer(pool, queries, machine, admins),
		auth:  authServiceHandler,
		event: service.NewEventServer(queries),
		webhook: service.NewWebhookServer(
			pool, queries, secretKeyProvider, ac.WebhooksAllowPrivate, ac.CountLimits,
		),
		org:       service.NewOrgServer(pool, queries, ac.Invitations, ac.CountLimits),
		workspace: service.NewWorkspaceServer(pool, queries, ac.CountLimits),
		resource: service.NewResourceServer(
			pool, queries, ac.ServiceDefaults, clusterSilentAfter, ac.CountLimits,
		),
		deployment: service.NewDeploymentServer(
			pool,
			queries,
			imageResolver,
			ac.RegistryHost,
			ac.ServiceDefaults,
			clusterSilentAfter,
			ac.SecretLimits.MaxPlacementBytes,
		),
		build: service.NewBuildServer(pool, queries, sourceBucket, service.BuildConfig{
			ClusterSilentAfter: clusterSilentAfter,
			RegistryHost:       ac.RegistryHost,
			RegistryPrefix:     ac.RegistryPrefix,
			SourceMaxBytes:     ac.SourceMaxBytes,
		}),
		domain: service.NewDomainServer(pool, queries, ac.ServiceDefaults, clusterSilentAfter, ac.CountLimits),
		token:  service.NewTokenServer(pool, queries, machine, ac.CountLimits),
		agent: service.NewAgentServer(
			pool,
			queries,
			placementNotifier,
			sourceBucket,
			secretKeyProvider,
			ac.HeartbeatInterval,
			ac.Agent,
		),
		ingest: service.NewIngestServer(
			queries,
			clusterAuthenticator,
			transitKeys.Ingest,
			ac.Ingest.TokenLifetime,
		),
		registry: registryServiceHandler,
		plan: service.NewPlanServer(
			pool,
			queries,
			imageResolver,
			ac.RegistryHost,
			ac.ServiceDefaults,
			ac.SecretLimits,
			clusterSilentAfter,
			ac.CountLimits,
		),
		environment: service.NewEnvironmentServer(pool, queries, ac.CountLimits),
		secret: service.NewSecretServer(
			pool, queries, secretKeyProvider, ac.SecretLimits, ac.CountLimits,
		),
		registryKey: registryKeyHandler,
	}, nil
}

func newRPCServers(ctx context.Context, ac *APIConfig, d *deps) (base, authenticated *connect.Server, err error) {
	deadlineInterceptor := interceptor.NewDeadlineInterceptor(ctx, ac.RPCTimeout)
	errorInterceptor := interceptor.NewErrorInterceptor()
	tracingInterceptor, err := newTracingInterceptor()
	if err != nil {
		return nil, nil, err
	}
	base = connect.NewServer(tracingInterceptor, errorInterceptor, deadlineInterceptor.Interceptor())
	authenticated = connect.NewServer(
		tracingInterceptor,
		errorInterceptor,
		deadlineInterceptor.Interceptor(),
		interceptor.NewContextInterceptor().Interceptor(),
		interceptor.NewAuthInterceptor(d.machine, d.verifier, d.resolver, d.authorizer).Interceptor(),
		validate.NewServerInterceptor(),
	)
	return base, authenticated, nil
}

func registerHandlers(mux *http.ServeMux, baseServer, authenticatedServer *connect.Server, d *deps) {
	mux.HandleFunc(healthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Server is healthy.")
	})

	configv1connect.RegisterConfigServiceHandler(baseServer, d.config)
	bootstrapv1connect.RegisterBootstrapServiceHandler(baseServer, d.bootstrap)
	agentv1connect.RegisterAgentServiceHandler(baseServer, d.agent)
	ingestv1connect.RegisterIngestServiceHandler(baseServer, d.ingest)
	registryv1connect.RegisterRegistryServiceHandler(baseServer, d.registry)

	clusterv1connect.RegisterClusterAdminServiceHandler(authenticatedServer, d.clusterAdmin)
	userv1connect.RegisterUserServiceHandler(authenticatedServer, d.user)
	authv1connect.RegisterAuthServiceHandler(authenticatedServer, d.auth)
	eventv1connect.RegisterEventServiceHandler(authenticatedServer, d.event)
	webhookv1connect.RegisterWebhookServiceHandler(authenticatedServer, d.webhook)
	orgv1connect.RegisterOrgServiceHandler(authenticatedServer, d.org)
	workspacev1connect.RegisterWorkspaceServiceHandler(authenticatedServer, d.workspace)
	resourcev1connect.RegisterResourceServiceHandler(authenticatedServer, d.resource)
	deploymentv1connect.RegisterDeploymentServiceHandler(authenticatedServer, d.deployment)
	buildv1connect.RegisterBuildServiceHandler(authenticatedServer, d.build)
	domainv1connect.RegisterDomainServiceHandler(authenticatedServer, d.domain)
	tokenv1connect.RegisterTokenServiceHandler(authenticatedServer, d.token)
	planv1connect.RegisterPlanServiceHandler(authenticatedServer, d.plan)
	environmentv1connect.RegisterEnvironmentServiceHandler(authenticatedServer, d.environment)
	secretv1connect.RegisterSecretServiceHandler(authenticatedServer, d.secret)

	mux.HandleFunc("GET "+ingesttoken.JWKSPath, d.ingest.JWKS)
	if d.registryKey != nil {
		mux.HandleFunc("GET "+registrytoken.KeyPath, d.registryKey)
		mux.HandleFunc("GET "+registrytoken.TokenPath, d.registry.Token)
		mux.HandleFunc("POST "+registrytoken.TokenPath, d.registry.Token)
	}
}

func startBackground(ctx context.Context, background *sync.WaitGroup, ac *APIConfig, d *deps) {
	background.Go(func() { d.machine.RunCleanup(ctx, ac.TokenCleanupInterval) })
	background.Go(func() { events.RunRetention(ctx, d.queries, ac.EventsRetention, eventsRetentionInterval) })
	background.Go(func() { d.webhookDispatcher.Run(ctx) })
	background.Go(func() { d.placementNotifier.Run(ctx) })
	if d.sourceSweeper != nil {
		background.Go(func() { d.sourceSweeper.Run(ctx) })
	}
	if d.imageSweeper != nil {
		background.Go(func() { d.imageSweeper.Run(ctx) })
	}
	if d.registrySweeper != nil {
		background.Go(func() { d.registrySweeper.Run(ctx) })
	}
}

func newServer(ac *APIConfig, mux *http.ServeMux) *http.Server {
	allowLoopback := ac.Env != envProduction
	corsMiddleware := withCORS(ac.CORSAllowedOrigins, allowLoopback)
	tracedMux := withServerTracing(mux)
	muxWCors := corsMiddleware(tracedMux)

	// Serve HTTP/1.1 alongside unencrypted HTTP/2 (h2c) using the stdlib
	// Protocols field; golang.org/x/net/http2/h2c is deprecated as of Go 1.26.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Server{
		Addr:              ac.Port,
		Handler:           muxWCors,
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// shutdown closes the pprof server, then drains server for up to drainTimeout before closing the
// connections still open.
func shutdown(server, pprofServer *http.Server, drainTimeout time.Duration) error {
	ctx := context.Background()
	drainCtx, cancel := context.WithTimeout(ctx, drainTimeout)
	defer cancel()

	if pprofServer != nil {
		if err := pprofServer.Close(); err != nil {
			slog.WarnContext(ctx, "failed to close pprof server", "error", err)
		}
	}

	if err := server.Shutdown(drainCtx); err != nil {
		slog.WarnContext(ctx, "graceful shutdown did not finish, closing remaining connections", "error", err)
		return server.Close()
	}
	slog.InfoContext(ctx, "server shutdown completed gracefully")
	return nil
}

func getLoggerHandler(ac *APIConfig) slog.Handler {
	if ac.Env == envProduction {
		return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     ac.LogLevel,
			AddSource: true,
		})
	}
	return charmLog.NewWithOptions(os.Stderr, charmLog.Options{ReportCaller: true, ReportTimestamp: true})
}

func mountRPCServers(mux *http.ServeMux, baseServer, authenticatedServer *connect.Server) {
	grpcreflect.Register(authenticatedServer, grpcreflect.WithNamer(grpcreflect.NamerFunc(func() []string {
		var names []string
		seen := map[string]bool{}
		for _, server := range []*connect.Server{baseServer, authenticatedServer} {
			for spec := range server.Specs() {
				serviceName, _, _ := strings.Cut(strings.TrimPrefix(spec.Procedure, "/"), "/")
				if !seen[serviceName] {
					names = append(names, serviceName)
					seen[serviceName] = true
				}
			}
		}
		return names
	})))
	connecthttp.Mount(mux, baseServer)
	connecthttp.Mount(mux, authenticatedServer)
}
