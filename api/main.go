package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/grpcreflect"
	"connectrpc.com/validate"
	charmLog "github.com/charmbracelet/log"
	"github.com/rs/cors"
	"github.com/team-loco/loco/api/db"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/middleware"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/pkg/commandbus"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/proto/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/proto/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/proto/loco/domain/v1/domainv1connect"
	environmentv1connect "github.com/team-loco/loco/proto/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/proto/loco/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/proto/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/proto/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/proto/loco/registry/v1/registryv1connect"
	"github.com/team-loco/loco/proto/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/proto/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/proto/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/proto/loco/workspace/v1/workspacev1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ApiConfig struct {
	Env                string // Environment (e.g., dev, prod)
	ProjectID          string // GitLab project ID
	GitlabURL          string // Container registry URL
	RegistryURL        string // Container registry URL
	DeployTokenName    string // Deploy token name
	GitlabPAT          string // GitLab Personal Access Token
	DatabaseURL        string // PostgreSQL connection string
	LogLevel           slog.Level
	Port               string
	RegistryTag        string
	CacheType          string   // Cache backend type: "in-memory" or "valkey"
	CacheAddr          string   // Valkey address (when CacheType is "valkey")
	CORSAllowedOrigins []string // CORS allowed origins (e.g., http://localhost:5173)
}

func newApiConfig() *ApiConfig {
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

	return &ApiConfig{
		Env:                os.Getenv("APP_ENV"),
		ProjectID:          os.Getenv("GITLAB_PROJECT_ID"),
		GitlabURL:          os.Getenv("GITLAB_URL"),
		RegistryURL:        os.Getenv("GITLAB_REGISTRY_URL"),
		DeployTokenName:    os.Getenv("GITLAB_DEPLOY_TOKEN_NAME"),
		GitlabPAT:          os.Getenv("GITLAB_PAT"),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		Port:               os.Getenv("APP_PORT"),
		LogLevel:           logLevel,
		RegistryTag:        os.Getenv("REGISTRY_TAG"),
		CacheType:          cacheType,
		CacheAddr:          os.Getenv("CACHE_ADDR"),
		CORSAllowedOrigins: corsOrigins,
	}
}

func newCache(cacheType, CacheAddr string, defaultTTL time.Duration) (cache.Cache, error) {
	switch cacheType {
	case "valkey":
		if CacheAddr == "" {
			return nil, fmt.Errorf("CACHE_ADDR required when CACHE_TYPE=valkey")
		}
		return cache.NewValkey(CacheAddr, defaultTTL)
	case "in-memory", "":
		return cache.NewBigCache(defaultTTL)
	default:
		return nil, fmt.Errorf("unknown cache type: %s", cacheType)
	}
}

func withCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	exposedHeaders := append(connectcors.ExposedHeaders(), "x-loco-request-id")
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

func main() {
	ac := newApiConfig()

	dbConn, err := db.NewDB(context.Background(), ac.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer dbConn.Close()

	pool := dbConn.Pool()
	queries := genDb.New(pool)

	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		MaxAPITokenDuration:         time.Hour * 24 * 365,
		SessionAccessTokenDuration:  time.Hour * 24,
		SessionRefreshTokenDuration: time.Hour * 24 * 30,
		LastUsedUpdateInterval:      time.Minute * 5,
	})

	logger := slog.New(CustomHandler{Handler: getLoggerHandler(ac)})
	slog.SetDefault(logger)

	mux := http.NewServeMux()
	interceptors := connect.WithInterceptors(middleware.NewGithubAuthInterceptor(machine), validate.NewInterceptor())

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Loco Service is Running")
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Server is healthy.")
	})

	appCache, err := newCache(ac.CacheType, ac.CacheAddr, 24*time.Hour)
	if err != nil {
		log.Fatalf("failed to create cache: %v", err)
	}
	defer appCache.Close()

	transport := &http.Transport{}
	http2Err := http2.ConfigureTransport(&http.Transport{})
	if http2Err != nil {
		panic("failed to configure HTTP/2 transport: " + http2Err.Error())
	}
	httpClient := &http.Client{Transport: transport}

	// Initialize command bus for agent communication
	cmdBus, err := commandbus.New(&commandbus.Config{
		Type:       "grpc",
		MaxRetries: 3,
	})
	if err != nil {
		log.Fatalf("failed to create command bus: %v", err)
	}
	defer cmdBus.Close()

	oauthStateCache := service.NewOAuthStateCache(appCache)
	oAuthServiceHandler := service.NewOAuthServer(pool, queries, httpClient, machine, oauthStateCache)
	userServiceHandler := service.NewUserServer(pool, queries, machine)
	orgServiceHandler := service.NewOrgServer(pool, queries, machine)
	workspaceServiceHandler := service.NewWorkspaceServer(pool, queries, machine)
	resourceServiceHandler := service.NewResourceServer(pool, queries, machine, cmdBus)
	deploymentServiceHandler := service.NewDeploymentServer(pool, queries, machine, cmdBus)
	domainServiceHandler := service.NewDomainServer(pool, queries, machine)
	tokenServiceHandler := service.NewTokenServer(pool, queries, machine)
	registryServiceHandler := service.NewRegistryServer(
		pool,
		queries,
		ac.GitlabURL,
		ac.GitlabPAT,
		ac.ProjectID,
		ac.DeployTokenName,
		ac.RegistryTag,
		httpClient,
		machine,
	)

	agentServiceHandler := service.NewAgentServer(pool, queries, cmdBus)
	observabilityAccessHandler := service.NewObservabilityAccessServer(pool, queries, machine)
	environmentServiceHandler := service.NewEnvironmentServer(pool, queries, machine)

	oauthPath, oauthHandler := oauthv1connect.NewOAuthServiceHandler(oAuthServiceHandler, interceptors)
	userPath, userHandler := userv1connect.NewUserServiceHandler(userServiceHandler, interceptors)
	orgPath, orgHandler := orgv1connect.NewOrgServiceHandler(orgServiceHandler, interceptors)
	workspacePath, workspaceHandler := workspacev1connect.NewWorkspaceServiceHandler(workspaceServiceHandler, interceptors)
	resourcePath, resourceHandler := resourcev1connect.NewResourceServiceHandler(resourceServiceHandler, interceptors)
	deploymentPath, deploymentHandler := deploymentv1connect.NewDeploymentServiceHandler(deploymentServiceHandler, interceptors)
	domainPath, domainHandler := domainv1connect.NewDomainServiceHandler(domainServiceHandler, interceptors)
	tokenPath, tokenHandler := tokenv1connect.NewTokenServiceHandler(tokenServiceHandler, interceptors)
	registryPath, registryHandler := registryv1connect.NewRegistryServiceHandler(registryServiceHandler, interceptors)
	agentPath, agentHandler := agentv1connect.NewAgentServiceHandler(agentServiceHandler)
	observabilityAccessPath, observabilityAccessH := observabilityv1connect.NewObservabilityAccessServiceHandler(observabilityAccessHandler, interceptors)
	environmentPath, environmentHandler := environmentv1connect.NewEnvironmentServiceHandler(environmentServiceHandler, interceptors)

	reflector := grpcreflect.NewStaticReflector(
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

		// registry service
		registryv1connect.RegistryServiceGetGitlabTokenProcedure,

		// agent service
		agentv1connect.AgentServiceRegisterProcedure,
		agentv1connect.AgentServiceCommandStreamProcedure,
		agentv1connect.AgentServiceHeartbeatProcedure,
		agentv1connect.AgentServiceReportStatusProcedure,

		// observability access service
		observabilityv1connect.ObservabilityAccessServiceGetObservabilityAccessProcedure,
		observabilityv1connect.ObservabilityAccessServiceCheckPermissionProcedure,

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
	muxWTiming := middleware.Timing(muxWCors)
	muxWContext := middleware.SetContext(muxWTiming)

	server := &http.Server{
		Addr:    ac.Port,
		Handler: h2c.NewHandler(muxWContext, &http2.Server{}),
	}

	quit := make(chan error, 1)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	go func() {
		ctx := context.Background()

		sig := <-sigChan
		slog.InfoContext(ctx, "shutdown signal received", "signal", sig.String())

		shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		machine.Close()

		if err := server.Shutdown(shutdownCtx); err != nil {
			quit <- err
			return
		}

		slog.InfoContext(shutdownCtx, "server shutdown completed gracefully")
		quit <- nil
	}()

	slog.Info("starting server", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		return
	}

	if err := <-quit; err != nil {
		log.Fatal(err)
	}
}

func getLoggerHandler(ac *ApiConfig) slog.Handler {
	if ac.Env == "PRODUCTION" {
		return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     ac.LogLevel,
			AddSource: true,
		})
	}
	return charmLog.NewWithOptions(os.Stderr, charmLog.Options{ReportCaller: true, ReportTimestamp: true})
}
