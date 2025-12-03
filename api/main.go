package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"
	"connectrpc.com/grpcreflect"
	charmLog "github.com/charmbracelet/log"
	"github.com/rs/cors"
	"github.com/team-loco/loco/api/db"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/middleware"
	"github.com/team-loco/loco/api/pkg/kube"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/shared"
	"github.com/team-loco/loco/shared/proto/app/v1/appv1connect"
	"github.com/team-loco/loco/shared/proto/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/shared/proto/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/shared/proto/org/v1/orgv1connect"
	"github.com/team-loco/loco/shared/proto/registry/v1/registryv1connect"
	"github.com/team-loco/loco/shared/proto/user/v1/userv1connect"
	"github.com/team-loco/loco/shared/proto/workspace/v1/workspacev1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type AppConfig struct {
	Env             string // Environment (e.g., dev, prod)
	ProjectID       string // GitLab project ID
	GitlabURL       string // Container registry URL
	RegistryURL     string // Container registry URL
	DeployTokenName string // Deploy token name
	GitlabPAT       string // GitLab Personal Access Token
	DatabaseURL     string // PostgreSQL connection string
	LogLevel        slog.Level
	Port            string
	JwtSecret       string
	RegistryTag     string
}

func newAppConfig() *AppConfig {
	logLevelStr := os.Getenv("LOG_LEVEL")
	logLevel := slog.LevelInfo
	if logLevelStr != "" {
		if parsed, err := strconv.Atoi(logLevelStr); err == nil {
			logLevel = slog.Level(parsed)
		}
	}

	return &AppConfig{
		Env:             os.Getenv("APP_ENV"),
		ProjectID:       os.Getenv("GITLAB_PROJECT_ID"),
		GitlabURL:       os.Getenv("GITLAB_URL"),
		RegistryURL:     os.Getenv("GITLAB_REGISTRY_URL"),
		DeployTokenName: os.Getenv("GITLAB_DEPLOY_TOKEN_NAME"),
		GitlabPAT:       os.Getenv("GITLAB_PAT"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		Port:            os.Getenv("PORT"),
		LogLevel:        logLevel,
		JwtSecret:       os.Getenv("JWT_SECRET"),
		RegistryTag:     os.Getenv("REGISTRY_TAG"),
	}
}

func withCORS(h http.Handler) http.Handler {
	middleware := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173"},
		AllowedMethods:   connectcors.AllowedMethods(),
		AllowedHeaders:   connectcors.AllowedHeaders(),
		ExposedHeaders:   connectcors.ExposedHeaders(),
		AllowCredentials: true,
		// AllowOriginFunc: func(origin string) bool {
		// 	return true
		// },
	})
	return middleware.Handler(h)
}

func main() {
	ac := newAppConfig()

	logger := slog.New(CustomHandler{Handler: getLoggerHandler(ac)})
	slog.SetDefault(logger)

	mux := http.NewServeMux()
	interceptors := connect.WithInterceptors(middleware.NewGithubAuthInterceptor())

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Loco Service is Running")
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "Server is healthy.")
	})

	kubeClient := kube.NewClient(ac.Env)

	dbConn, err := db.NewDB(context.Background(), ac.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer dbConn.Close()

	pool := dbConn.Pool()
	queries := genDb.New(pool)

	httpClient := shared.NewHTTPClient()

	oAuthServiceHandler, err := service.NewOAuthServer(pool, queries, httpClient)
	if err != nil {
		log.Fatal(err)
	}
	userServiceHandler := service.NewUserServer(pool, queries)
	orgServiceHandler := service.NewOrgServer(pool, queries)
	workspaceServiceHandler := service.NewWorkspaceServer(pool, queries)
	appServiceHandler := service.NewAppServer(pool, queries, kubeClient)
	deploymentServiceHandler := service.NewDeploymentServer(pool, queries, kubeClient)
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
	appPath, appHandler := appv1connect.NewAppServiceHandler(appServiceHandler, interceptors)
	deploymentPath, deploymentHandler := deploymentv1connect.NewDeploymentServiceHandler(deploymentServiceHandler, interceptors)
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

		// app service
		appv1connect.AppServiceCreateAppProcedure,
		appv1connect.AppServiceGetAppProcedure,
		appv1connect.AppServiceListAppsProcedure,
		appv1connect.AppServiceUpdateAppProcedure,
		appv1connect.AppServiceDeleteAppProcedure,
		appv1connect.AppServiceCheckSubdomainAvailabilityProcedure,

		// deployment service
		deploymentv1connect.DeploymentServiceCreateDeploymentProcedure,
		deploymentv1connect.DeploymentServiceGetDeploymentProcedure,
		deploymentv1connect.DeploymentServiceListDeploymentsProcedure,
		deploymentv1connect.DeploymentServiceStreamDeploymentProcedure,

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
	mux.Handle(appPath, appHandler)
	mux.Handle(deploymentPath, deploymentHandler)
	mux.Handle(registryPath, registryHandler)

	muxWCors := withCORS(mux)
	muxWTiming := middleware.Timing(muxWCors)
	muxWContext := middleware.SetContext(muxWTiming)

	log.Fatal(http.ListenAndServe(
		":8000",
		h2c.NewHandler(muxWContext, &http2.Server{}),
	))
}

func getLoggerHandler(ac *AppConfig) slog.Handler {
	if ac.Env == "PRODUCTION" {
		return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     ac.LogLevel,
			AddSource: true,
		})
	}
	return charmLog.NewWithOptions(os.Stderr, charmLog.Options{ReportCaller: true, ReportTimestamp: true})
}
