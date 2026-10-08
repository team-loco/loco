package main

import (
	"os"
	"regexp"
	"strconv"

	railway "github.com/railwayapp/railway-go-sdk"
)

const (
	registry     = "ghcr.io/team-loco"
	region       = "us-east4-eqdc4a"
	uiPort       = 8080
	bucketRegion = "iad"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type environment struct {
	commit       string
	domainPrefix string
	uiTagSuffix  string
}

func environmentFor(ctx railway.Context) environment {
	commit := os.Getenv("DEPLOY_COMMIT")
	if !commitPattern.MatchString(commit) {
		panic("DEPLOY_COMMIT must be the full commit sha whose images to deploy, got " + strconv.Quote(commit))
	}
	if ctx.IsEnvironment("staging") {
		return environment{commit: commit, domainPrefix: "staging.", uiTagSuffix: "-staging"}
	}
	return environment{commit: commit, domainPrefix: "", uiTagSuffix: ""}
}

func image(name string, tag string) map[string]any {
	return railway.Image(registry + "/" + name + ":" + tag)
}

func preserved(names ...string) map[string]any {
	out := make(map[string]any, len(names))
	for _, name := range names {
		out[name] = railway.Preserve()
	}
	return out
}

func volume(name string) railway.Resource {
	return railway.Volume(name, map[string]any{
		"alerts": map[string]any{
			"usage": map[string]any{
				"80":  map[string]any{},
				"95":  map[string]any{},
				"100": map[string]any{},
			},
		},
		"allowOnlineResize": true,
		"region":            region,
		"sizeMB":            5000,
	})
}

func bucket(name string) railway.Resource {
	return railway.Bucket(name, map[string]any{"region": bucketRegion})
}

func limits(cpu float64, memoryBytes int) map[string]any {
	return map[string]any{
		"limitOverride": map[string]any{
			"containers": map[string]any{
				"cpu":         cpu,
				"memoryBytes": memoryBytes,
			},
		},
		"restartPolicyMaxRetries": 1,
	}
}

func dockerBuild(dockerfile string) map[string]any {
	return map[string]any{
		"buildEnvironment": "V3",
		"builder":          "DOCKERFILE",
		"dockerfilePath":   dockerfile,
	}
}

func ui(env environment) railway.Service {
	uiDomain := env.domainPrefix + "loco.build"
	docsDomain := "docs." + uiDomain
	domains := []any{
		map[string]any{"domain": uiDomain, "port": uiPort},
		map[string]any{"domain": docsDomain, "port": uiPort},
	}
	return railway.ServiceNamed("loco::cp-ui", railway.ServiceConfig{
		"source":   image("loco-ui", "sha-"+env.commit+env.uiTagSuffix),
		"domains":  domains,
		"build":    dockerBuild("/web/Dockerfile"),
		"replicas": map[string]any{region: 2},
		"deploy":   limits(0.5, 1000000000),
		"networking": map[string]any{
			"privateNetworkEndpoint": "captivating-wisdom",
		},
	})
}

func api(env environment, sources railway.Resource) railway.Service {
	apiEnv := preserved(
		"APP_ENV",
		"APP_PORT",
		"CACHE_ADDR",
		"CACHE_TYPE",
		"CORS_ALLOWED_ORIGINS",
		"DATABASE_URL",
		"GH_OAUTH_CLIENT_ID",
		"GH_OAUTH_CLIENT_SECRET",
		"LOG_LEVEL",
	)
	apiEnv["MIN_CLI_VERSION"] = "v0.0.61"
	apiEnv["PORT"] = "8000"
	apiEnv["RAILWAY_DEPLOYMENT_DRAINING_SECONDS"] = "40"
	apiEnv["LOCO_REGISTRY_HOST"] = registryDomain(env)
	apiEnv["LOCO_SOURCE_BUCKET"] = sources.Env("BUCKET")
	apiEnv["LOCO_SOURCE_BUCKET_ENDPOINT"] = sources.Env("ENDPOINT")
	apiEnv["LOCO_SOURCE_BUCKET_REGION"] = sources.Env("REGION")
	apiEnv["LOCO_SOURCE_BUCKET_ACCESS_KEY_ID"] = sources.Env("ACCESS_KEY_ID")
	apiEnv["LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY"] = sources.Env("SECRET_ACCESS_KEY")
	deploy := limits(1, 2000000000)
	deploy["healthcheckPath"] = "/health"
	deploy["healthcheckTimeout"] = 300
	return railway.ServiceNamed("loco::cp-api", railway.ServiceConfig{
		"source":   image("loco-api", "sha-"+env.commit),
		"build":    dockerBuild("/api/Dockerfile"),
		"replicas": map[string]any{region: 2},
		"deploy":   deploy,
		"networking": map[string]any{
			"privateNetworkEndpoint": "loco",
			"customDomains": map[string]any{
				"api." + env.domainPrefix + "loco.build": map[string]any{"port": 8000},
			},
		},
		"env": apiEnv,
	})
}

func registryDomain(env environment) string {
	return "registry." + env.domainPrefix + "loco.build"
}

func imageRegistry(env environment, storage railway.Resource) railway.Service {
	registryEnv := preserved("ZOT_HTPASSWD")
	registryEnv["S3_BUCKET"] = storage.Env("BUCKET")
	registryEnv["S3_ENDPOINT"] = storage.Env("ENDPOINT")
	registryEnv["S3_REGION"] = storage.Env("REGION")
	registryEnv["AWS_ACCESS_KEY_ID"] = storage.Env("ACCESS_KEY_ID")
	registryEnv["AWS_SECRET_ACCESS_KEY"] = storage.Env("SECRET_ACCESS_KEY")
	deploy := limits(1, 1000000000)
	deploy["healthcheckPath"] = "/readyz"
	deploy["healthcheckTimeout"] = 120
	return railway.ServiceNamed("loco::registry", railway.ServiceConfig{
		"source":   image("loco-registry", "sha-"+env.commit),
		"build":    dockerBuild("/registry/Dockerfile"),
		"replicas": map[string]any{region: 1},
		"deploy":   deploy,
		"env":      registryEnv,
	})
}

func cache(data railway.Resource) railway.Service {
	return railway.ServiceNamed("loco::cp-cache", railway.ServiceConfig{
		"source":   railway.Image("valkey/valkey:latest"),
		"start":    `/bin/sh -c "exec docker-entrypoint.sh valkey-server --port ${VALKEY_PORT} --requirepass ${VALKEY_PASSWORD}"`,
		"replicas": map[string]any{region: 1},
		"networking": map[string]any{
			"privateNetworkEndpoint": "valkey",
			"tcpProxies":             map[string]any{"6379": map[string]any{}},
		},
		"volumeMounts": map[string]any{"/data": data},
		"env": preserved(
			"VALKEY_HOST",
			"VALKEY_PASSWORD",
			"VALKEY_PORT",
			"VALKEY_PUBLIC_HOST",
			"VALKEY_PUBLIC_PORT",
			"VALKEY_PUBLIC_URL",
			"VALKEY_URL",
			"VALKEY_USER",
		),
	})
}

func productionDB(data railway.Resource) railway.Service {
	return railway.ServiceNamed("loco::cp-db", railway.ServiceConfig{
		"source": railway.Github("castab/postgres-18-ssl", map[string]any{
			"commitSha":   "13ed8cc401350081291a79d1df1eb4fa9663edba",
			"upstreamUrl": "https://github.com/castab/postgres-18-ssl",
		}),
		"replicas": map[string]any{region: 1},
		"networking": map[string]any{
			"privateNetworkEndpoint": "postgres-18-ssl",
			"tcpProxies":             map[string]any{"5432": map[string]any{}},
		},
		"volumeMounts": map[string]any{"/var/lib/postgresql": data},
		"env": preserved(
			"DATABASE_PUBLIC_URL",
			"DATABASE_URL",
			"PGDATA",
			"PGDATABASE",
			"PGHOST",
			"PGPASSWORD",
			"PGPORT",
			"PGUSER",
			"POSTGRES_DB",
			"POSTGRES_PASSWORD",
			"POSTGRES_USER",
			"RAILWAY_DEPLOYMENT_DRAINING_SECONDS",
			"SSL_CERT_DAYS",
		),
	})
}

func Railway(ctx railway.Context) railway.Project {
	ctx = railway.NewContext(ctx)
	env := environmentFor(ctx)
	cacheData := volume("valkey-volume")
	dbData := volume("postgres-18-ssl-volume")
	registryStorage := bucket("registry-storage")
	buildSources := bucket("build-sources")
	resources := []any{
		ui(env),
		api(env, buildSources),
		imageRegistry(env, registryStorage),
		cache(cacheData),
		cacheData,
		dbData,
		registryStorage,
		buildSources,
	}
	if ctx.IsEnvironment("staging") {
		resources = append(resources, railway.Postgres("loco::cp-db-staging", map[string]any{"region": region}))
	} else {
		resources = append(resources, productionDB(dbData))
	}
	return railway.ProjectNamed("loco", resources)
}
