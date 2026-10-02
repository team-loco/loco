package main

import (
	railway "github.com/railwayapp/railway-go-sdk"
)

const (
	repo   = "team-loco/loco"
	region = "us-east4-eqdc4a"
)

type environment struct {
	branch       string
	domainPrefix string
}

func environmentFor(ctx railway.Context) environment {
	if ctx.IsEnvironment("staging") {
		return environment{branch: "staging", domainPrefix: "staging."}
	}
	return environment{branch: "main", domainPrefix: ""}
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
	return railway.ServiceNamed("loco::cp-ui", railway.ServiceConfig{
		"source": railway.Github(repo, map[string]any{
			"branch":        env.branch,
			"checkSuites":   false,
			"rootDirectory": "/web",
		}),
		"build":      dockerBuild("/web/Dockerfile"),
		"replicas":   map[string]any{region: 2},
		"deploy":     limits(0.5, 1000000000),
		"domains":    []any{env.domainPrefix + "loco.build"},
		"networking": map[string]any{"privateNetworkEndpoint": "captivating-wisdom"},
		"env":        preserved("VITE_API_URL", "VITE_APP_ENV"),
	})
}

func api(env environment) railway.Service {
	return railway.ServiceNamed("loco::cp-api", railway.ServiceConfig{
		"source": railway.Github(repo, map[string]any{
			"branch":      env.branch,
			"checkSuites": false,
		}),
		"build":    dockerBuild("/api/Dockerfile"),
		"replicas": map[string]any{region: 2},
		"deploy":   limits(1, 2000000000),
		"domains": []any{
			map[string]any{"domain": "api." + env.domainPrefix + "loco.build", "port": 8000},
		},
		"networking": map[string]any{"privateNetworkEndpoint": "loco"},
		"env": preserved(
			"APP_ENV",
			"APP_PORT",
			"CACHE_ADDR",
			"CACHE_TYPE",
			"CORS_ALLOWED_ORIGINS",
			"DATABASE_URL",
			"GH_OAUTH_CLIENT_ID",
			"GH_OAUTH_CLIENT_SECRET",
			"GH_OAUTH_STATE",
			"GITLAB_PAT",
			"GITLAB_PROJECT_ID",
			"GITLAB_REGISTRY_URL",
			"GITLAB_TOKEN_NAME",
			"GITLAB_URL",
			"LOG_LEVEL",
			"PORT",
			"REGISTRY_TAG",
		),
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
	resources := []any{ui(env), api(env), cache(cacheData), cacheData, dbData}
	if ctx.IsEnvironment("staging") {
		resources = append(resources, railway.Postgres("loco::cp-db-staging", map[string]any{"region": region}))
	} else {
		resources = append(resources, productionDB(dbData))
	}
	return railway.ProjectNamed("loco", resources)
}
