package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/grpcreflect"
	charmLog "github.com/charmbracelet/log"

	"github.com/rs/cors"
	"github.com/team-loco/loco/api/db"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/middleware"
	"github.com/team-loco/loco/api/pkg/kube"
	"github.com/team-loco/loco/api/pkg/statuswatcher"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/shared"
	"github.com/team-loco/loco/shared/proto/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/shared/proto/domain/v1/domainv1connect"
	"github.com/team-loco/loco/shared/proto/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/shared/proto/org/v1/orgv1connect"
	"github.com/team-loco/loco/shared/proto/registry/v1/registryv1connect"
	"github.com/team-loco/loco/shared/proto/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/shared/proto/user/v1/userv1connect"
	"github.com/team-loco/loco/shared/proto/workspace/v1/workspacev1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ApiConfig struct {
	Env             string // Environment (e.g., dev, prod)
	ProjectID       string // GitLab project ID
	GitlabURL       string // Container registry URL
	RegistryURL     string // Container registry URL
	DeployTokenName string // Deploy token name
	GitlabPAT       string // GitLab Personal Access Token
	DatabaseURL     string // PostgreSQL connection string
	LogLevel        slog.Level
	Port            string
	RegistryTag     string
	LocoNamespace   string // Loco system namespace
	LocoDomainBase  string // Base domain (e.g., deploy-app.com)
	LocoDomainAPI   string // API domain (e.g., api.deploy-app.com)
}

func newApiConfig() *ApiConfig {
	logLevelStr := os.Getenv("LOG_LEVEL")
	logLevel := slog.LevelInfo
	if logLevelStr != "" {
		if parsed, err := strconv.Atoi(logLevelStr); err == nil {
			logLevel = slog.Level(parsed)
		}
	}

	return &ApiConfig{
		Env:             os.Getenv("APP_ENV"),
		ProjectID:       os.Getenv("GITLAB_PROJECT_ID"),
		GitlabURL:       os.Getenv("GITLAB_URL"),
		RegistryURL:     os.Getenv("GITLAB_REGISTRY_URL"),
		DeployTokenName: os.Getenv("GITLAB_DEPLOY_TOKEN_NAME"),
		GitlabPAT:       os.Getenv("GITLAB_PAT"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		Port:            os.Getenv("PORT"),
		LogLevel:        logLevel,
		RegistryTag:     os.Getenv("REGISTRY_TAG"),
		LocoNamespace:   os.Getenv("LOCO_NAMESPACE"),
		LocoDomainBase:  os.Getenv("LOCO_DOMAIN_BASE"),
		LocoDomainAPI:   os.Getenv("LOCO_DOMAIN_API"),
	}
}

func isAllowedOrigin(hostname, baseDomain string) bool {
	if hostname == "localhost" {
		return true
	}
	if baseDomain == "" {
		return false
	}
	return hostname == baseDomain || hostname == "www."+baseDomain
}

func withCORS(baseDomain string) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		middleware := cors.New(cors.Options{
			AllowOriginFunc: func(origin string) bool {
				u, err := url.Parse(origin)
				if err != nil {
					return false
				}
				return isAllowedOrigin(u.Hostname(), baseDomain)
			},
			AllowedMethods:   connectcors.AllowedMethods(),
			AllowedHeaders:   connectcors.AllowedHeaders(),
			ExposedHeaders:   connectcors.ExposedHeaders(),
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

	tvm := tvm.NewVendingMachine(pool, queries, tvm.Config{
		MaxTokenDuration:   time.Hour * 24 * 30,
		LoginTokenDuration: time.Hour * 1,
	})

	logger := slog.New(CustomHandler{Handler: getLoggerHandler(ac)})
	slog.SetDefault(logger)

	mux := http.NewServeMux()
	interceptors := connect.WithInterceptors(middleware.NewGithubAuthInterceptor(tvm))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Loco Service is Running")
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Server is healthy.")
	})

	kubeClient := kube.NewClient(ac.Env)

	watcher := statuswatcher.NewStatusWatcher(kubeClient, queries)
	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()

	go func() {
		if err := watcher.Start(watcherCtx); err != nil {
			slog.Error("status watcher failed", "error", err)
		}
	}()

	httpClient := shared.NewHTTPClient()

	oAuthServiceHandler, err := service.NewOAuthServer(pool, queries, httpClient, tvm)
	if err != nil {
		log.Fatal(err)
	}
	userServiceHandler := service.NewUserServer(pool, queries, tvm)
	orgServiceHandler := service.NewOrgServer(pool, queries, tvm)
	workspaceServiceHandler := service.NewWorkspaceServer(pool, queries, tvm)
	resourceServiceHandler := service.NewResourceServer(pool, queries, tvm, kubeClient, ac.LocoNamespace)
	deploymentServiceHandler := service.NewDeploymentServer(pool, queries, tvm, kubeClient, ac.LocoNamespace)
	domainServiceHandler := service.NewDomainServer(pool, queries, tvm)
	registryServiceHandler := service.NewRegistryServer(
		pool,
		queries,
		ac.GitlabURL,
		ac.GitlabPAT,
		ac.ProjectID,
		ac.DeployTokenName,
		ac.RegistryTag,
		httpClient,
	)

	oauthPath, oauthHandler := oauthv1connect.NewOAuthServiceHandler(oAuthServiceHandler, interceptors)
	userPath, userHandler := userv1connect.NewUserServiceHandler(userServiceHandler, interceptors)
	orgPath, orgHandler := orgv1connect.NewOrgServiceHandler(orgServiceHandler, interceptors)
	workspacePath, workspaceHandler := workspacev1connect.NewWorkspaceServiceHandler(workspaceServiceHandler, interceptors)
	resourcePath, resourceHandler := resourcev1connect.NewResourceServiceHandler(resourceServiceHandler, interceptors)
	deploymentPath, deploymentHandler := deploymentv1connect.NewDeploymentServiceHandler(deploymentServiceHandler, interceptors)
	domainPath, domainHandler := domainv1connect.NewDomainServiceHandler(domainServiceHandler, interceptors)
	registryPath, registryHandler := registryv1connect.NewRegistryServiceHandler(registryServiceHandler, interceptors)

	reflector := grpcreflect.NewStaticReflector(
		// oauth service
		oauthv1connect.OAuthServiceGithubOAuthDetailsProcedure,
		oauthv1connect.OAuthServiceExchangeGithubTokenProcedure,
		oauthv1connect.OAuthServiceGetGithubAuthorizationURLProcedure,
		oauthv1connect.OAuthServiceExchangeGithubCodeProcedure,

		// user service
		userv1connect.UserServiceCreateUserProcedure,
		userv1connect.UserServiceGetUserProcedure,
		userv1connect.UserServiceGetCurrentUserProcedure,
		userv1connect.UserServiceUpdateUserProcedure,
		userv1connect.UserServiceListUsersProcedure,
		userv1connect.UserServiceDeleteUserProcedure,

		// org service
		orgv1connect.OrgServiceCreateOrgProcedure,
		orgv1connect.OrgServiceGetOrgProcedure,
		orgv1connect.OrgServiceGetCurrentUserOrgsProcedure,
		orgv1connect.OrgServiceListOrgsProcedure,
		orgv1connect.OrgServiceUpdateOrgProcedure,
		orgv1connect.OrgServiceDeleteOrgProcedure,
		orgv1connect.OrgServiceIsUniqueOrgNameProcedure,

		// workspace service
		workspacev1connect.WorkspaceServiceCreateWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceGetWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceGetUserWorkspacesProcedure,
		workspacev1connect.WorkspaceServiceListWorkspacesProcedure,
		workspacev1connect.WorkspaceServiceUpdateWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceDeleteWorkspaceProcedure,
		workspacev1connect.WorkspaceServiceAddMemberProcedure,
		workspacev1connect.WorkspaceServiceRemoveMemberProcedure,
		workspacev1connect.WorkspaceServiceListMembersProcedure,

		// resource service
		resourcev1connect.ResourceServiceCreateResourceProcedure,
		resourcev1connect.ResourceServiceGetResourceProcedure,
		resourcev1connect.ResourceServiceListResourcesProcedure,
		resourcev1connect.ResourceServiceUpdateResourceProcedure,
		resourcev1connect.ResourceServiceDeleteResourceProcedure,

		// deployment service
		deploymentv1connect.DeploymentServiceCreateDeploymentProcedure,
		deploymentv1connect.DeploymentServiceGetDeploymentProcedure,
		deploymentv1connect.DeploymentServiceListDeploymentsProcedure,
		deploymentv1connect.DeploymentServiceStreamDeploymentProcedure,

		// domain service
		domainv1connect.DomainServiceCreatePlatformDomainProcedure,
		domainv1connect.DomainServiceGetPlatformDomainProcedure,
		domainv1connect.DomainServiceGetPlatformDomainByNameProcedure,
		domainv1connect.DomainServiceListActivePlatformDomainsProcedure,
		domainv1connect.DomainServiceDeactivatePlatformDomainProcedure,
		domainv1connect.DomainServiceCheckDomainAvailabilityProcedure,
		domainv1connect.DomainServiceListAllLocoOwnedDomainsProcedure,

		// registry service
		registryv1connect.RegistryServiceGitlabTokenProcedure,
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
	mux.Handle(registryPath, registryHandler)

	muxWCors := withCORS(ac.LocoDomainBase)(mux)
	muxWTiming := middleware.Timing(muxWCors)
	muxWContext := middleware.SetContext(muxWTiming)

	server := &http.Server{
		Addr:    ":8000",
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

		// stop the k8s resources watcher and tvm
		watcherCancel()
		tvm.Close()

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
