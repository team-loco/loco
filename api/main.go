package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	charmLog "charm.land/log/v2"
	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/grpcreflect"
	"connectrpc.com/validate"
	"github.com/rs/cors"
	"github.com/team-loco/loco/api/db"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/migrations"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/pkg/clusternotify"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1connect "github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"golang.org/x/mod/semver"
)

const envProduction = "PRODUCTION"

type APIConfig struct {
	Env                   string // Environment (e.g., dev, prod)
	ProjectID             string // GitLab project ID
	GitlabURL             string // Container registry URL
	RegistryURL           string // Container registry URL
	GitlabPAT             string // GitLab Personal Access Token
	DatabaseURL           string // PostgreSQL connection string
	LogLevel              slog.Level
	Port                  string
	RegistryTag           string
	CacheType             string   // Cache backend type: "in-memory" or "valkey"
	CacheAddr             string   // Valkey address (when CacheType is "valkey")
	CORSAllowedOrigins    []string // CORS allowed origins (e.g., http://localhost:5173)
	DefaultPlatformDomain string   // Default platform domain returned by the config service
	MinCLIVersion         string
	PprofAddr             string
}

func newAPIConfig() *APIConfig {
	logLevelStr := os.Getenv("LOG_LEVEL")
	logLevel := slog.LevelInfo
	if logLevelStr != "" {
		if parsed, err := strconv.Atoi(logLevelStr); err == nil {
			logLevel = slog.Level(parsed)
		}
	}

	cacheType := os.Getenv("CACHE_TYPE")
	if cacheType == "" {
		cacheType = "in-memory"
	}

	corsOriginsStr := os.Getenv("CORS_ALLOWED_ORIGINS")
	corsOrigins := []string{}
	if corsOriginsStr != "" {
		corsOrigins = strings.Split(corsOriginsStr, ",")
		for i := range corsOrigins {
			corsOrigins[i] = strings.TrimSpace(corsOrigins[i])
		}
	}

	return &APIConfig{
		Env:                   os.Getenv("APP_ENV"),
		ProjectID:             os.Getenv("GITLAB_PROJECT_ID"),
		GitlabURL:             os.Getenv("GITLAB_URL"),
		RegistryURL:           os.Getenv("GITLAB_REGISTRY_URL"),
		GitlabPAT:             os.Getenv("GITLAB_PAT"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		Port:                  os.Getenv("APP_PORT"),
		LogLevel:              logLevel,
		RegistryTag:           os.Getenv("REGISTRY_TAG"),
		CacheType:             cacheType,
		CacheAddr:             os.Getenv("CACHE_ADDR"),
		CORSAllowedOrigins:    corsOrigins,
		DefaultPlatformDomain: os.Getenv("DEFAULT_PLATFORM_DOMAIN"),
		MinCLIVersion:         os.Getenv("MIN_CLI_VERSION"),
		PprofAddr:             os.Getenv("PPROF_ADDR"),
	}
}

func newCache(cacheType, CacheAddr string, defaultTTL time.Duration) (cache.Cache, error) {
	switch cacheType {
	case "valkey":
		if CacheAddr == "" {
			return nil, fmt.Errorf("CACHE_ADDR required when CACHE_TYPE=valkey")
		}
		return cache.NewValkey(CacheAddr, defaultTTL)
	case "in-memory":
		return cache.NewMemory(defaultTTL)
	case "":
		return cache.NewMemory(defaultTTL)
	default:
		return nil, fmt.Errorf("unknown cache type: %s", cacheType)
	}
}

func withCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	exposedHeaders := append(connectcors.ExposedHeaders(), "x-loco-request-id", "server-timing")
	return func(h http.Handler) http.Handler {
		middleware := cors.New(cors.Options{
			AllowedOrigins:   allowedOrigins,
			AllowedMethods:   connectcors.AllowedMethods(),
			AllowedHeaders:   connectcors.AllowedHeaders(),
			ExposedHeaders:   exposedHeaders,
			AllowCredentials: true,
		})
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

	if ac.MinCLIVersion != "" && !semver.IsValid(ac.MinCLIVersion) {
		log.Fatalf("MIN_CLI_VERSION %q is not a semantic version like v0.0.61", ac.MinCLIVersion)
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
	httpInterceptors := connect.WithInterceptors(
		deadlineInterceptor,
		interceptor.NewContextInterceptor(),
		interceptor.NewGithubAuthInterceptor(machine),
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

	httpClient := newOutboundHTTPClient()

	placementNotifier := clusternotify.New(pool, clusternotify.DefaultPollInterval)
	if err := placementNotifier.Start(shutdownCtx); err != nil {
		log.Fatalf("failed to start placement listener: %v", err)
	}

	oauthStateCache := service.NewOAuthStateCache(appCache)
	secureCookies := ac.Env == envProduction
	oAuthServiceHandler := service.NewOAuthServer(pool, queries, httpClient, machine, oauthStateCache, secureCookies)
	userServiceHandler := service.NewUserServer(pool, queries, machine, secureCookies)
	orgServiceHandler := service.NewOrgServer(pool, queries, machine)
	workspaceServiceHandler := service.NewWorkspaceServer(pool, queries, machine)
	resourceServiceHandler := service.NewResourceServer(pool, queries, machine)
	deploymentServiceHandler := service.NewDeploymentServer(pool, queries, machine)
	domainServiceHandler := service.NewDomainServer(pool, queries, machine)
	tokenServiceHandler := service.NewTokenServer(pool, queries, machine)
	registryServiceHandler := service.NewRegistryServer(
		pool,
		queries,
		ac.GitlabURL,
		ac.GitlabPAT,
		ac.ProjectID,
		ac.RegistryTag,
		httpClient,
		machine,
	)

	agentServiceHandler := service.NewAgentServer(pool, queries, placementNotifier)
	observabilityAccessHandler := service.NewObservabilityAccessServer(pool, queries, machine)
	environmentServiceHandler := service.NewEnvironmentServer(pool, queries, machine)
	configServiceHandler := service.NewConfigServer(ac.DefaultPlatformDomain, ac.MinCLIVersion)

	configPath, configHandler := configv1connect.NewConfigServiceHandler(configServiceHandler, baseInterceptors)
	oauthPath, oauthHandler := oauthv1connect.NewOAuthServiceHandler(oAuthServiceHandler, httpInterceptors)
	userPath, userHandler := userv1connect.NewUserServiceHandler(userServiceHandler, httpInterceptors)
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
	domainPath, domainHandler := domainv1connect.NewDomainServiceHandler(domainServiceHandler, httpInterceptors)
	tokenPath, tokenHandler := tokenv1connect.NewTokenServiceHandler(tokenServiceHandler, httpInterceptors)
	registryPath, registryHandler := registryv1connect.NewRegistryServiceHandler(
		registryServiceHandler,
		httpInterceptors,
	)
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
		configv1connect.ConfigServiceGetDefaultServiceConfigProcedure,
		configv1connect.ConfigServiceGetServerInfoProcedure,

		// oauth service
		oauthv1connect.OAuthServiceGetOAuthDetailsProcedure,
		oauthv1connect.OAuthServiceExchangeOAuthTokenProcedure,
		oauthv1connect.OAuthServiceGetOAuthAuthorizationURLProcedure,
		oauthv1connect.OAuthServiceExchangeOAuthCodeProcedure,

		// user service
		userv1connect.UserServiceCreateUserProcedure,
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

		// registry service
		registryv1connect.RegistryServiceGetGitlabTokenProcedure,
		registryv1connect.RegistryServiceGetImageRepositoryProcedure,

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
	mux.Handle(oauthPath, oauthHandler)
	mux.Handle(userPath, userHandler)
	mux.Handle(orgPath, orgHandler)
	mux.Handle(workspacePath, workspaceHandler)
	mux.Handle(resourcePath, resourceHandler)
	mux.Handle(deploymentPath, deploymentHandler)
	mux.Handle(domainPath, domainHandler)
	mux.Handle(tokenPath, tokenHandler)
	mux.Handle(registryPath, registryHandler)
	mux.Handle(agentPath, agentHandler)
	mux.Handle(observabilityAccessPath, observabilityAccessH)
	mux.Handle(environmentPath, environmentHandler)

	muxWCors := withCORS(ac.CORSAllowedOrigins)(mux)

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
