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
	"github.com/team-loco/loco/api/pkg/imageresolver"
	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/gen/go/loco/domain/v1/domainv1connect"
	environmentv1connect "github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	"github.com/team-loco/loco/gen/go/loco/oauth/v1/oauthv1connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/gen/go/loco/workspace/v1/workspacev1connect"
	"golang.org/x/mod/semver"
)

var (
	errCacheAddrMissing   = errors.New("CACHE_ADDR required when CACHE_TYPE=valkey")
	errUnknownCacheType   = errors.New("unknown cache type")
	errInvalidSourceBytes = errors.New("LOCO_SOURCE_MAX_BYTES is not a positive integer")

	errInvalidForcePathStyle = errors.New("LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE is not a boolean")
	errInvalidInt32          = errors.New("is not a 32-bit integer")
	errInvalidServiceDefault = errors.New("invalid service default")
	errInvalidDuration       = errors.New("is not a duration")
	errNotPositive           = errors.New("must be positive")
	errRegistryAuthPartial   = errors.New("LOCO_REGISTRY_USERNAME and LOCO_REGISTRY_PASSWORD must be set together")
	errRegistryAuthNoHost    = errors.New("LOCO_REGISTRY_USERNAME is set without LOCO_REGISTRY_HOST")
	errInvalidRegistry       = errors.New("invalid registry configuration")
)

const (
	envProduction         = "PRODUCTION"
	cacheTypeValkey       = "valkey"
	cacheTypeMemory       = "in-memory"
	defaultSourceMaxBytes = 200 * 1024 * 1024
	imageResolveTimeout   = 15 * time.Second

	defaultServiceCPU         = "100m"
	defaultServiceMemory      = "256Mi"
	defaultServiceMinReplicas = 1
	defaultServiceMaxReplicas = 1
	defaultServicePathPrefix  = "/"
	defaultServiceIdleTimeout = 60

	defaultRegistryScheme            = "https://"
	defaultRegistryTimeout           = 30 * time.Second
	defaultImageRetention            = 5
	defaultImageSweepInterval        = 10 * time.Minute
	defaultImageSweepBuildBatch      = 100
	defaultImageSweepRepositoryBatch = 100
)

var loopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

type APIConfig struct {
	Env                   string // Environment (e.g., dev, prod)
	DatabaseURL           string // PostgreSQL connection string
	LogLevel              slog.Level
	Port                  string
	CacheType             string   // Cache backend type: "in-memory" or "valkey"
	CacheAddr             string   // Valkey address (when CacheType is "valkey")
	CORSAllowedOrigins    []string // CORS allowed origins (e.g., http://localhost:5173)
	DefaultPlatformDomain string   // Default platform domain returned by the config service
	MinCLIVersion         string
	PprofAddr             string
	SourceBucket          sourcebucket.Config
	SourceMaxBytes        int64
	RegistryHost          string
	RegistryPrefix        string
	Registry              registryclient.Config
	ImageSweep            service.ImageSweepConfig
	ServiceDefaults       servicedefaults.Defaults
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
		cacheType = cacheTypeMemory
	}
	cacheAddr := os.Getenv("CACHE_ADDR")
	if cacheType != cacheTypeValkey && cacheType != cacheTypeMemory {
		panic(fmt.Errorf("%w: %q", errUnknownCacheType, cacheType))
	}
	if cacheType == cacheTypeValkey && cacheAddr == "" {
		panic(errCacheAddrMissing)
	}

	corsOriginsStr := os.Getenv("CORS_ALLOWED_ORIGINS")
	corsOrigins := []string{}
	if corsOriginsStr != "" {
		corsOrigins = strings.Split(corsOriginsStr, ",")
		for i := range corsOrigins {
			corsOrigins[i] = strings.TrimSpace(corsOrigins[i])
		}
	}

	sourceMaxBytes := int64(defaultSourceMaxBytes)
	if raw := os.Getenv("LOCO_SOURCE_MAX_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			panic(fmt.Errorf("%w: %q", errInvalidSourceBytes, raw))
		}
		sourceMaxBytes = parsed
	}

	forcePathStyle := false
	if raw := os.Getenv("LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			panic(fmt.Errorf("%w: %q", errInvalidForcePathStyle, raw))
		}
		forcePathStyle = parsed
	}

	sourceBucket := sourcebucket.Config{
		Endpoint:        os.Getenv("LOCO_SOURCE_BUCKET_ENDPOINT"),
		Bucket:          os.Getenv("LOCO_SOURCE_BUCKET"),
		Region:          os.Getenv("LOCO_SOURCE_BUCKET_REGION"),
		AccessKeyID:     os.Getenv("LOCO_SOURCE_BUCKET_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY"),
		ForcePathStyle:  forcePathStyle,
	}
	if sourceBucket.Bucket != "" {
		if err := sourceBucket.Validate(); err != nil {
			panic(err)
		}
	}

	serviceDefaults := servicedefaults.Defaults{
		CPU:         stringEnv("LOCO_DEFAULT_CPU", defaultServiceCPU),
		Memory:      stringEnv("LOCO_DEFAULT_MEMORY", defaultServiceMemory),
		MinReplicas: int32Env("LOCO_DEFAULT_MIN_REPLICAS", defaultServiceMinReplicas),
		MaxReplicas: int32Env("LOCO_DEFAULT_MAX_REPLICAS", defaultServiceMaxReplicas),
		PathPrefix:  stringEnv("LOCO_DEFAULT_PATH_PREFIX", defaultServicePathPrefix),
		IdleTimeout: int32Env("LOCO_DEFAULT_IDLE_TIMEOUT", defaultServiceIdleTimeout),
	}
	if err := serviceDefaults.Validate(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidServiceDefault, err))
	}

	registryHost := os.Getenv("LOCO_REGISTRY_HOST")
	registryPrefix := os.Getenv("LOCO_REGISTRY_PREFIX")
	registry := newRegistryConfig(registryHost)
	imageSweep := service.ImageSweepConfig{
		RegistryHost:    registryHost,
		RegistryPrefix:  registryPrefix,
		Retention:       positiveInt32Env("LOCO_IMAGE_RETENTION", defaultImageRetention),
		Interval:        positiveDurationEnv("LOCO_IMAGE_SWEEP_INTERVAL", defaultImageSweepInterval),
		BuildBatch:      positiveInt32Env("LOCO_IMAGE_SWEEP_BUILD_BATCH", defaultImageSweepBuildBatch),
		RepositoryBatch: int(positiveInt32Env("LOCO_IMAGE_SWEEP_REPOSITORY_BATCH", defaultImageSweepRepositoryBatch)),
	}

	return &APIConfig{
		Env:                   os.Getenv("APP_ENV"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		Port:                  os.Getenv("APP_PORT"),
		LogLevel:              logLevel,
		CacheType:             cacheType,
		CacheAddr:             cacheAddr,
		CORSAllowedOrigins:    corsOrigins,
		DefaultPlatformDomain: os.Getenv("DEFAULT_PLATFORM_DOMAIN"),
		MinCLIVersion:         os.Getenv("MIN_CLI_VERSION"),
		PprofAddr:             os.Getenv("PPROF_ADDR"),
		SourceBucket:          sourceBucket,
		SourceMaxBytes:        sourceMaxBytes,
		RegistryHost:          registryHost,
		RegistryPrefix:        registryPrefix,
		Registry:              registry,
		ImageSweep:            imageSweep,
		ServiceDefaults:       serviceDefaults,
	}
}

func newRegistryConfig(registryHost string) registryclient.Config {
	cfg := registryclient.Config{
		URL:      stringEnv("LOCO_REGISTRY_URL", defaultRegistryScheme+registryHost),
		Username: os.Getenv("LOCO_REGISTRY_USERNAME"),
		Password: os.Getenv("LOCO_REGISTRY_PASSWORD"),
		Timeout:  positiveDurationEnv("LOCO_REGISTRY_TIMEOUT", defaultRegistryTimeout),
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		panic(errRegistryAuthPartial)
	}
	if cfg.Username == "" {
		return cfg
	}
	if registryHost == "" {
		panic(errRegistryAuthNoHost)
	}
	if err := cfg.Validate(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidRegistry, err))
	}
	return cfg
}

func positiveDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, errInvalidDuration))
	}
	if parsed <= 0 {
		panic(fmt.Errorf("%s %q %w", name, raw, errNotPositive))
	}
	return parsed
}

func positiveInt32Env(name string, fallback int32) int32 {
	value := int32Env(name, fallback)
	if value <= 0 {
		panic(fmt.Errorf("%s %d %w", name, value, errNotPositive))
	}
	return value
}

func stringEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func int32Env(name string, fallback int32) int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, errInvalidInt32))
	}
	return int32(parsed)
}

func newSourceBucket(cfg sourcebucket.Config) (service.SourceBucket, error) {
	if cfg.Bucket == "" {
		slog.Warn("LOCO_SOURCE_BUCKET is not set; builds are disabled")
		return nil, nil
	}
	bucket, err := sourcebucket.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("source bucket: %w", err)
	}
	return bucket, nil
}

func newImageRegistry(cfg registryclient.Config) (service.ImageRegistry, error) {
	if cfg.Username == "" {
		slog.Warn("LOCO_REGISTRY_USERNAME is not set; build images are never deleted from the registry")
		return nil, nil
	}
	client, err := registryclient.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("registry client: %w", err)
	}
	return client, nil
}

func newCache(cacheType, cacheAddr string, defaultTTL time.Duration) (cache.Cache, error) {
	switch cacheType {
	case cacheTypeValkey:
		return cache.NewValkey(cacheAddr, defaultTTL)
	case cacheTypeMemory:
		return cache.NewMemory(defaultTTL)
	default:
		return nil, fmt.Errorf("%w: %q", errUnknownCacheType, cacheType)
	}
}

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
		AllowedHeaders:   connectcors.AllowedHeaders(),
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
	resourceServiceHandler := service.NewResourceServer(pool, queries, machine, ac.ServiceDefaults)
	sourceBucket, bucketErr := newSourceBucket(ac.SourceBucket)
	if bucketErr != nil {
		log.Fatal(bucketErr)
	}
	if ac.RegistryHost == "" {
		slog.Warn("LOCO_REGISTRY_HOST is not set; builds are disabled")
	}
	if sourceBucket != nil {
		sourceSweeper := service.NewSourceSweeper(pool, queries, sourceBucket)
		go sourceSweeper.Run(shutdownCtx)
	}
	imageRegistry, registryErr := newImageRegistry(ac.Registry)
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
		machine,
		imageResolver,
		ac.RegistryHost,
		ac.ServiceDefaults,
	)
	buildServiceHandler := service.NewBuildServer(pool, queries, machine, sourceBucket, service.BuildConfig{
		RegistryHost:   ac.RegistryHost,
		RegistryPrefix: ac.RegistryPrefix,
		SourceMaxBytes: ac.SourceMaxBytes,
	})
	domainServiceHandler := service.NewDomainServer(pool, queries, machine)
	tokenServiceHandler := service.NewTokenServer(pool, queries, machine)
	agentServiceHandler := service.NewAgentServer(pool, queries, placementNotifier, sourceBucket)
	observabilityAccessHandler := service.NewObservabilityAccessServer(pool, queries, machine)
	environmentServiceHandler := service.NewEnvironmentServer(pool, queries, machine)
	configServiceHandler := service.NewConfigServer(ac.DefaultPlatformDomain, ac.MinCLIVersion, ac.ServiceDefaults)

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

		// oauth service
		oauthv1connect.OAuthServiceGetOAuthDetailsProcedure,
		oauthv1connect.OAuthServiceExchangeOAuthTokenProcedure,
		oauthv1connect.OAuthServiceGetOAuthAuthorizationURLProcedure,
		oauthv1connect.OAuthServiceExchangeOAuthCodeProcedure,

		// user service
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
	mux.Handle(oauthPath, oauthHandler)
	mux.Handle(userPath, userHandler)
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
