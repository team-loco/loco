package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	charmLog "charm.land/log/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/grpcreflect/v2"
	"connectrpc.com/validate"
	"github.com/rs/cors"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/supabase"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/migrations"
	"github.com/team-loco/loco/api/pkg/clusternotify"
	"github.com/team-loco/loco/api/pkg/imageresolver"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/webhooks"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	"github.com/team-loco/loco/gen/go/loco/cluster/v1/clusterv1connect"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1connect "github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	planv1connect "github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/secret/v1/secretv1connect"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/gen/go/loco/webhook/v1/webhookv1connect"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
)

const (
	envProduction           = "PRODUCTION"
	imageResolveTimeout     = 15 * time.Second
	eventsRetentionInterval = time.Hour
)

var loopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

var version string

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return slices.Contains(loopbackHosts, u.Hostname())
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
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}
}

func main() {
	ac := newAPIConfig()

	logger := slog.New(CustomHandler{Handler: getLoggerHandler(ac)})
	slog.SetDefault(logger)
	slog.Info("starting loco api", "version", ac.Version)

	if err := migrations.Up(context.Background(), ac.DatabaseURL); err != nil {
		log.Fatal(err)
	}

	dbConn, err := db.NewDB(context.Background(), ac.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer dbConn.Close()

	pool := dbConn.Pool()
	queries := genDb.New(pool)
	if syncErr := webhooks.SyncInstallWebhooks(context.Background(), pool, ac.InstallWebhooks); syncErr != nil {
		log.Fatalf("INSTALL_WEBHOOKS: %v", syncErr)
	}

	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		MaxAPITokenDuration:         time.Hour * 24 * 365,
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour * 24 * 7,
		LastUsedUpdateInterval:      time.Minute * 5,
	})

	shutdownCtx, beginShutdown := context.WithCancel(context.Background())
	defer beginShutdown()

	deadlineInterceptor := interceptor.NewDeadlineInterceptor(shutdownCtx, 30*time.Second)
	errorInterceptor := interceptor.NewErrorInterceptor()
	baseServer := connect.NewServer(errorInterceptor, deadlineInterceptor.Interceptor())

	mux := http.NewServeMux()
	authClient := newOutboundHTTPClient()
	adminFactories := auth.AdminFactories{supabase.AdminType: supabase.NewAdmin}
	admins, adminsErr := auth.NewAdmins(authClient, ac.AuthIssuers, adminFactories)
	if adminsErr != nil {
		log.Fatalf("AUTH_ISSUERS admin: %v", adminsErr)
	}
	emailVerifiers, emailVerifiersErr := auth.NewEmailVerifiers(ac.AuthIssuers, admins)
	if emailVerifiersErr != nil {
		log.Fatalf("AUTH_ISSUERS email verification: %v", emailVerifiersErr)
	}
	verifier := auth.NewVerifier(authClient, ac.AuthIssuers)
	emailVerifierOption := auth.WithEmailVerifiers(emailVerifiers)
	resolver := auth.NewResolver(pool, ac.SignupPolicy, emailVerifierOption)

	authenticatedServer := connect.NewServer(
		errorInterceptor,
		deadlineInterceptor.Interceptor(),
		interceptor.NewContextInterceptor().Interceptor(),
		interceptor.NewAuthInterceptor(machine, verifier, resolver, authz.New(pool, queries)).Interceptor(),
		validate.NewServerInterceptor(),
	)

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Loco Service is Running")
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Server is healthy.")
	})

	appCache, err := newCache(ac.CacheType, ac.CacheAddr, 24*time.Hour)
	if err != nil {
		log.Fatalf("failed to create cache: %v", err)
	}
	defer appCache.Close()

	placementNotifier := clusternotify.New(ac.DatabaseListenConfig, clusternotify.DefaultPollInterval)
	if err := placementNotifier.Start(shutdownCtx); err != nil {
		log.Fatalf("failed to start placement listener: %v", err)
	}

	eventServiceHandler := service.NewEventServer(queries, machine)
	go events.RunRetention(shutdownCtx, queries, ac.EventsRetention, eventsRetentionInterval)
	webhookServiceHandler := service.NewWebhookServer(pool, queries, ac.WebhooksAllowPrivate)
	workspaceWebhookClient := webhooks.NewClient(ac.WebhooksAllowPrivate)
	installWebhookClient := webhooks.NewClient(true)
	webhookDispatcher := webhooks.NewDispatcher(queries, workspaceWebhookClient, installWebhookClient)
	go webhookDispatcher.Run(shutdownCtx, webhooks.DefaultPollPeriod)
	authServiceHandler := service.NewAuthServer(pool, queries, machine, appCache, admins, ac.WebURL)
	userServiceHandler := service.NewUserServer(pool, queries, machine, admins)
	orgServiceHandler := service.NewOrgServer(pool, queries, machine)
	workspaceServiceHandler := service.NewWorkspaceServer(pool, queries, machine)
	clusterSilentAfter := service.ClusterSilentAfter(ac.HeartbeatInterval)
	clusterAdminServiceHandler := service.NewClusterAdminServer(pool, queries)
	resourceServiceHandler := service.NewResourceServer(pool, queries, ac.ServiceDefaults, clusterSilentAfter)
	sourceBucket, bucketErr := newSourceBucket(ac.SourceBucket)
	if bucketErr != nil {
		log.Fatal(bucketErr)
	}
	if ac.RegistryHost == "" {
		slog.Warn("LOCO_REGISTRY_HOST is not set; builds are disabled")
	}
	if sourceBucket != nil {
		sourceSweeper := service.NewSourceSweeper(pool, queries, sourceBucket, ac.SourceSweep)
		go sourceSweeper.Run(shutdownCtx)
	}
	imageRegistry, registryErr := newImageRegistry(ac.Registry, ac.ImageSweep.Retention)
	if registryErr != nil {
		log.Fatal(registryErr)
	}
	if imageRegistry != nil {
		imageSweeper := service.NewImageSweeper(pool, queries, imageRegistry, ac.ImageSweep)
		go imageSweeper.Run(shutdownCtx)
	}
	imageResolver := imageresolver.New(imageResolveTimeout)

	deploymentServiceHandler := service.NewDeploymentServer(
		pool,
		queries,
		imageResolver,
		ac.RegistryHost,
		ac.ServiceDefaults,
		clusterSilentAfter,
	)
	buildServiceHandler := service.NewBuildServer(pool, queries, sourceBucket, service.BuildConfig{
		RegistryHost:   ac.RegistryHost,
		RegistryPrefix: ac.RegistryPrefix,
		SourceMaxBytes: ac.SourceMaxBytes,
	})
	domainServiceHandler := service.NewDomainServer(pool, queries, machine, ac.ServiceDefaults)
	tokenServiceHandler := service.NewTokenServer(pool, queries, machine)
	secretKeyProvider, providerErr := newSecretKeyProvider(shutdownCtx, ac.Secrets)
	if providerErr != nil {
		log.Fatalf("failed to create the secrets key provider: %v", providerErr)
	}
	agentServiceHandler := service.NewAgentServer(
		pool,
		queries,
		placementNotifier,
		sourceBucket,
		secretKeyProvider,
		ac.HeartbeatInterval,
		ac.Agent,
	)
	environmentServiceHandler := service.NewEnvironmentServer(pool, queries, machine)
	secretServiceHandler := service.NewSecretServer(pool, queries, secretKeyProvider, ac.SecretLimits)
	configServiceHandler := service.NewConfigServer(
		ac.DefaultPlatformDomain,
		ac.MinCLIVersion,
		ac.SchemaURL,
		ac.ServiceDefaults,
		ac.AuthIssuers,
	)

	configv1connect.RegisterConfigServiceHandler(baseServer, configServiceHandler)
	clusterv1connect.RegisterClusterAdminServiceHandler(authenticatedServer, clusterAdminServiceHandler)
	userv1connect.RegisterUserServiceHandler(authenticatedServer, userServiceHandler)
	authv1connect.RegisterAuthServiceHandler(authenticatedServer, authServiceHandler)
	eventv1connect.RegisterEventServiceHandler(authenticatedServer, eventServiceHandler)
	webhookv1connect.RegisterWebhookServiceHandler(authenticatedServer, webhookServiceHandler)
	orgv1connect.RegisterOrgServiceHandler(authenticatedServer, orgServiceHandler)
	workspacev1connect.RegisterWorkspaceServiceHandler(authenticatedServer, workspaceServiceHandler)
	resourcev1connect.RegisterResourceServiceHandler(authenticatedServer, resourceServiceHandler)
	deploymentv1connect.RegisterDeploymentServiceHandler(authenticatedServer, deploymentServiceHandler)
	buildv1connect.RegisterBuildServiceHandler(authenticatedServer, buildServiceHandler)
	domainv1connect.RegisterDomainServiceHandler(authenticatedServer, domainServiceHandler)
	tokenv1connect.RegisterTokenServiceHandler(authenticatedServer, tokenServiceHandler)
	agentv1connect.RegisterAgentServiceHandler(baseServer, agentServiceHandler)
	planServiceHandler := service.NewPlanServer(
		pool,
		queries,
		imageResolver,
		ac.RegistryHost,
		ac.ServiceDefaults,
		ac.SecretLimits,
		clusterSilentAfter,
	)
	planv1connect.RegisterPlanServiceHandler(authenticatedServer, planServiceHandler)
	environmentv1connect.RegisterEnvironmentServiceHandler(authenticatedServer, environmentServiceHandler)
	secretv1connect.RegisterSecretServiceHandler(authenticatedServer, secretServiceHandler)

	mountRPCServers(mux, baseServer, authenticatedServer)
	mountObservabilityProxy(mux, ac.ObservabilityProxyURL)

	allowLoopback := ac.Env != envProduction
	corsMiddleware := withCORS(ac.CORSAllowedOrigins, allowLoopback)
	muxWCors := corsMiddleware(mux)

	// Serve HTTP/1.1 alongside unencrypted HTTP/2 (h2c) using the stdlib
	// Protocols field; golang.org/x/net/http2/h2c is deprecated as of Go 1.26.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	server := &http.Server{
		Addr:              ac.Port,
		Handler:           muxWCors,
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	var pprofServer *http.Server
	if ac.PprofAddr != "" {
		pprofServer = newPprofServer(ac.PprofAddr)
		go func() {
			slog.Info("starting pprof server", "addr", pprofServer.Addr)
			if err := pprofServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("pprof server error", "error", err)
			}
		}()
	}

	quit := make(chan error, 1)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	go func() {
		ctx := context.Background()

		sig := <-sigChan
		slog.InfoContext(ctx, "shutdown signal received", "signal", sig.String())

		beginShutdown()

		drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		machine.Close()

		if pprofServer != nil {
			if err := pprofServer.Close(); err != nil {
				slog.WarnContext(ctx, "failed to close pprof server", "error", err)
			}
		}

		if err := server.Shutdown(drainCtx); err != nil {
			slog.WarnContext(ctx, "graceful shutdown did not finish, closing remaining connections", "error", err)
			if closeErr := server.Close(); closeErr != nil {
				quit <- closeErr
				return
			}
			quit <- nil
			return
		}

		slog.InfoContext(ctx, "server shutdown completed gracefully")
		quit <- nil
	}()

	slog.Info("starting server", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server error", "error", err)
		return
	}

	if err := <-quit; err != nil {
		log.Fatal(err)
	}
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
		for spec := range baseServer.Specs() {
			serviceName, _, _ := strings.Cut(strings.TrimPrefix(spec.Procedure, "/"), "/")
			if !seen[serviceName] {
				names = append(names, serviceName)
				seen[serviceName] = true
			}
		}
		for spec := range authenticatedServer.Specs() {
			serviceName, _, _ := strings.Cut(strings.TrimPrefix(spec.Procedure, "/"), "/")
			if !seen[serviceName] {
				names = append(names, serviceName)
				seen[serviceName] = true
			}
		}
		return names
	})))
	connecthttp.Mount(mux, baseServer)
	connecthttp.Mount(mux, authenticatedServer)
}
