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
	"strconv"
	"syscall"
	"time"

	charmLog "charm.land/log/v2"
	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/grpcreflect"
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
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1connect "github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/gen/go/loco/webhook/v1/webhookv1connect"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"golang.org/x/mod/semver"
)

const (
	envProduction       = "PRODUCTION"
	imageResolveTimeout = 15 * time.Second
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
	exposedHeaders := append(connectcors.ExposedHeaders(), "x-loco-request-id", "server-timing")
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

func eventsRetention(days string) time.Duration {
	n, err := strconv.Atoi(days)
	if err != nil || n <= 0 {
		return 90 * 24 * time.Hour
	}
	return time.Duration(n) * 24 * time.Hour
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

	if ac.MinCLIVersion != "" && !semver.IsValid(ac.MinCLIVersion) {
		log.Fatalf("MIN_CLI_VERSION %q is not a semantic version like v0.0.61", ac.MinCLIVersion)
	}

	issuers, issuersErr := auth.ParseIssuers(ac.AuthIssuers)
	if issuersErr != nil {
		log.Fatalf("AUTH_ISSUERS: %v", issuersErr)
	}
	signupPolicy, policyErr := auth.ParseSignupPolicy(ac.AuthSignupMode, ac.AuthSignupDomains)
	if policyErr != nil {
		log.Fatalf("AUTH_SIGNUP_MODE: %v", policyErr)
	}
	installWebhooks, installWebhooksErr := webhooks.ParseInstallWebhooks(ac.InstallWebhooks)
	if installWebhooksErr != nil {
		log.Fatalf("INSTALL_WEBHOOKS: %v", installWebhooksErr)
	}

	if err := migrations.Up(context.Background(), ac.DatabaseURL); err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return
	}

	dbConn, err := db.NewDB(context.Background(), ac.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer dbConn.Close()

	pool := dbConn.Pool()
	queries := genDb.New(pool)
	if syncErr := webhooks.SyncInstallWebhooks(context.Background(), pool, installWebhooks); syncErr != nil {
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
	baseInterceptors := connect.WithInterceptors(deadlineInterceptor)

	mux := http.NewServeMux()
	authClient := newOutboundHTTPClient()
	adminFactories := auth.AdminFactories{supabase.AdminType: supabase.NewAdmin}
	admins, adminsErr := auth.NewAdmins(authClient, issuers, os.Getenv, adminFactories)
	if adminsErr != nil {
		log.Fatalf("AUTH_ISSUERS admin: %v", adminsErr)
	}
	emailVerifiers, emailVerifiersErr := auth.NewEmailVerifiers(issuers, admins)
	if emailVerifiersErr != nil {
		log.Fatalf("AUTH_ISSUERS email verification: %v", emailVerifiersErr)
	}
	verifier := auth.NewVerifier(authClient, issuers)
	emailVerifierOption := auth.WithEmailVerifiers(emailVerifiers)
	resolver := auth.NewResolver(pool, signupPolicy, emailVerifierOption)

	httpInterceptors := connect.WithInterceptors(
		deadlineInterceptor,
		interceptor.NewContextInterceptor(),
		interceptor.NewAuthInterceptor(machine, verifier, resolver, authz.New(pool, queries)),
		validate.NewInterceptor(),
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

	placementNotifier := clusternotify.New(pool, clusternotify.DefaultPollInterval)
	if err := placementNotifier.Start(shutdownCtx); err != nil {
		log.Fatalf("failed to start placement listener: %v", err)
	}

	eventServiceHandler := service.NewEventServer(queries, machine)
	go events.RunRetention(shutdownCtx, queries, eventsRetention(ac.EventsRetentionDays), time.Hour)
	webhookServiceHandler := service.NewWebhookServer(pool, queries, ac.WebhooksAllowPrivate)
	workspaceWebhookClient := webhooks.NewClient(ac.WebhooksAllowPrivate)
	installWebhookClient := webhooks.NewClient(true)
	webhookDispatcher := webhooks.NewDispatcher(queries, workspaceWebhookClient, installWebhookClient)
	go webhookDispatcher.Run(shutdownCtx, webhooks.DefaultPollPeriod)
	authServiceHandler := service.NewAuthServer(pool, queries, machine, appCache, admins, ac.WebURL)
	userServiceHandler := service.NewUserServer(pool, queries, machine, admins)
	orgServiceHandler := service.NewOrgServer(pool, queries, machine)
	workspaceServiceHandler := service.NewWorkspaceServer(pool, queries, machine)
	resourceServiceHandler := service.NewResourceServer(pool, queries, ac.ServiceDefaults)
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
	)
	buildServiceHandler := service.NewBuildServer(pool, queries, sourceBucket, service.BuildConfig{
		RegistryHost:   ac.RegistryHost,
		RegistryPrefix: ac.RegistryPrefix,
		SourceMaxBytes: ac.SourceMaxBytes,
	})
	domainServiceHandler := service.NewDomainServer(pool, queries, machine)
	tokenServiceHandler := service.NewTokenServer(pool, queries, machine)
	agentServiceHandler := service.NewAgentServer(pool, queries, placementNotifier, sourceBucket)
	observabilityAccessHandler := service.NewObservabilityAccessServer(pool, queries, machine)
	environmentServiceHandler := service.NewEnvironmentServer(pool, queries, machine)
	configServiceHandler := service.NewConfigServer(
		ac.DefaultPlatformDomain,
		ac.MinCLIVersion,
		ac.ServiceDefaults,
		issuers,
	)

	configPath, configHandler := configv1connect.NewConfigServiceHandler(configServiceHandler, baseInterceptors)
	userPath, userHandler := userv1connect.NewUserServiceHandler(userServiceHandler, httpInterceptors)
	authPath, authHandler := authv1connect.NewAuthServiceHandler(authServiceHandler, httpInterceptors)
	eventPath, eventHandler := eventv1connect.NewEventServiceHandler(eventServiceHandler, httpInterceptors)
	webhookPath, webhookHandler := webhookv1connect.NewWebhookServiceHandler(webhookServiceHandler, httpInterceptors)
	orgPath, orgHandler := orgv1connect.NewOrgServiceHandler(orgServiceHandler, httpInterceptors)
	workspacePath, workspaceHandler := workspacev1connect.NewWorkspaceServiceHandler(
		workspaceServiceHandler,
		httpInterceptors,
	)
	resourcePath, resourceHandler := resourcev1connect.NewResourceServiceHandler(
		resourceServiceHandler,
		httpInterceptors,
	)
	deploymentPath, deploymentHandler := deploymentv1connect.NewDeploymentServiceHandler(
		deploymentServiceHandler,
		httpInterceptors,
	)
	buildPath, buildHandler := buildv1connect.NewBuildServiceHandler(buildServiceHandler, httpInterceptors)
	domainPath, domainHandler := domainv1connect.NewDomainServiceHandler(domainServiceHandler, httpInterceptors)
	tokenPath, tokenHandler := tokenv1connect.NewTokenServiceHandler(tokenServiceHandler, httpInterceptors)
	agentPath, agentHandler := agentv1connect.NewAgentServiceHandler(agentServiceHandler, baseInterceptors)
	observabilityAccessPath, observabilityAccessH := observabilityv1connect.NewObservabilityAccessServiceHandler(
		observabilityAccessHandler,
		httpInterceptors,
	)
	environmentPath, environmentHandler := environmentv1connect.NewEnvironmentServiceHandler(
		environmentServiceHandler,
		httpInterceptors,
	)

	reflector := grpcreflect.NewStaticReflector(
		// config service
		configv1connect.ConfigServiceGetConfigProcedure,

		// user service
		eventv1connect.EventServiceListOrgEventsProcedure,
		eventv1connect.EventServiceStreamEventsProcedure,
		webhookv1connect.WebhookServiceCreateWebhookProcedure,
		webhookv1connect.WebhookServiceListWebhooksProcedure,
		webhookv1connect.WebhookServiceDeleteWebhookProcedure,
		webhookv1connect.WebhookServiceListWebhookDeliveriesProcedure,
		authv1connect.AuthServiceApproveCLILoginProcedure,
		authv1connect.AuthServiceExchangeCLICodeProcedure,
		authv1connect.AuthServiceStartDeviceLoginProcedure,
		authv1connect.AuthServiceApproveDeviceLoginProcedure,
		authv1connect.AuthServicePollDeviceLoginProcedure,
		authv1connect.AuthServiceRefreshCLITokenProcedure,
		userv1connect.UserServiceGetUserProcedure,
		userv1connect.UserServiceWhoAmIProcedure,
		userv1connect.UserServiceUpdateUserProcedure,
		userv1connect.UserServiceListUsersProcedure,
		userv1connect.UserServiceDeleteUserProcedure,

		// org service
		orgv1connect.OrgServiceCreateOrgProcedure,
		orgv1connect.OrgServiceGetOrgProcedure,
		orgv1connect.OrgServiceListUserOrgsProcedure,
		orgv1connect.OrgServiceListOrgUsersProcedure,
		orgv1connect.OrgServiceListOrgWorkspacesProcedure,
		orgv1connect.OrgServiceUpdateOrgProcedure,
		orgv1connect.OrgServiceDeleteOrgProcedure,

		// workspace service
		workspacev1connect.WorkspaceServiceCreateWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceGetWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceListUserWorkspacesProcedure,
		workspacev1connect.WorkspaceServiceListOrgWorkspacesProcedure,
		workspacev1connect.WorkspaceServiceUpdateWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceDeleteWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceCreateMemberProcedure,
		workspacev1connect.WorkspaceServiceDeleteMemberProcedure,
		workspacev1connect.WorkspaceServiceListWorkspaceMembersProcedure,

		// resource service
		resourcev1connect.ResourceServiceCreateResourceProcedure,
		resourcev1connect.ResourceServiceGetResourceProcedure,
		resourcev1connect.ResourceServiceListWorkspaceResourcesProcedure,
		resourcev1connect.ResourceServiceUpdateResourceProcedure,
		resourcev1connect.ResourceServiceDeleteResourceProcedure,
		resourcev1connect.ResourceServiceListResourceEventsProcedure,

		// deployment service
		deploymentv1connect.DeploymentServiceCreateDeploymentProcedure,
		deploymentv1connect.DeploymentServiceGetDeploymentProcedure,
		deploymentv1connect.DeploymentServiceListDeploymentsProcedure,
		deploymentv1connect.DeploymentServiceWatchDeploymentProcedure,

		buildv1connect.BuildServiceCreateBuildProcedure,
		buildv1connect.BuildServiceStartBuildProcedure,
		buildv1connect.BuildServiceGetBuildProcedure,
		buildv1connect.BuildServiceListBuildsProcedure,
		buildv1connect.BuildServiceCancelBuildProcedure,

		// domain service
		domainv1connect.DomainServiceCreatePlatformDomainProcedure,
		domainv1connect.DomainServiceGetPlatformDomainProcedure,
		domainv1connect.DomainServiceListPlatformDomainsProcedure,
		domainv1connect.DomainServiceUpdatePlatformDomainProcedure,
		domainv1connect.DomainServiceDeletePlatformDomainProcedure,
		domainv1connect.DomainServiceCreateResourceDomainProcedure,
		domainv1connect.DomainServiceUpdateResourceDomainProcedure,
		domainv1connect.DomainServiceSetPrimaryResourceDomainProcedure,
		domainv1connect.DomainServiceDeleteResourceDomainProcedure,
		domainv1connect.DomainServiceListLocoOwnedDomainsProcedure,
		domainv1connect.DomainServiceCheckDomainAvailabilityProcedure,

		// token service
		tokenv1connect.TokenServiceCreateTokenProcedure,
		tokenv1connect.TokenServiceListTokensProcedure,
		tokenv1connect.TokenServiceGetTokenProcedure,
		tokenv1connect.TokenServiceRevokeTokenProcedure,
		tokenv1connect.TokenServiceGetScopesProcedure,
		tokenv1connect.TokenServiceCheckPermissionProcedure,

		// agent service
		agentv1connect.AgentServiceRegisterProcedure,
		agentv1connect.AgentServiceSyncProcedure,
		agentv1connect.AgentServiceHeartbeatProcedure,

		// observability access service
		observabilityv1connect.ObservabilityAccessServiceGetObservabilityAccessProcedure,

		// environment service
		environmentv1connect.EnvironmentServiceCreateEnvironmentProcedure,
		environmentv1connect.EnvironmentServiceGetEnvironmentProcedure,
		environmentv1connect.EnvironmentServiceListEnvironmentsProcedure,
		environmentv1connect.EnvironmentServiceUpdateEnvironmentProcedure,
		environmentv1connect.EnvironmentServiceDeleteEnvironmentProcedure,
	)

	// mount both old and new reflectors for backwards compatibility
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	mux.Handle(configPath, configHandler)
	mux.Handle(userPath, userHandler)
	mux.Handle(authPath, authHandler)
	mux.Handle(eventPath, eventHandler)
	mux.Handle(webhookPath, webhookHandler)
	mux.Handle(orgPath, orgHandler)
	mux.Handle(workspacePath, workspaceHandler)
	mux.Handle(resourcePath, resourceHandler)
	mux.Handle(deploymentPath, deploymentHandler)
	mux.Handle(buildPath, buildHandler)
	mux.Handle(domainPath, domainHandler)
	mux.Handle(tokenPath, tokenHandler)
	mux.Handle(agentPath, agentHandler)
	mux.Handle(observabilityAccessPath, observabilityAccessH)
	mux.Handle(environmentPath, environmentHandler)

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
