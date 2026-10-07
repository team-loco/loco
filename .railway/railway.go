package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"

	railway "github.com/railwayapp/railway-go-sdk"
)

const (
	registry           = "ghcr.io/team-loco"
	region             = "us-east4-eqdc4a"
	authPort           = 9999
	hookSecret         = "AUTH_HOOK_SECRET"
	accessTokenSeconds = "600"
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
		"source":   image("loco-ui", "sha-"+env.commit+env.uiTagSuffix),
		"build":    dockerBuild("/web/Dockerfile"),
		"replicas": map[string]any{region: 2},
		"deploy":   limits(0.5, 1000000000),
		"networking": map[string]any{
			"privateNetworkEndpoint": "captivating-wisdom",
			"customDomains": map[string]any{
				env.domainPrefix + "loco.build": map[string]any{"port": 8080},
			},
		},
	})
}

func (env environment) webURL() string {
	return "https://" + env.domainPrefix + "loco.build"
}

func (env environment) apiURL() string {
	return "https://api." + env.domainPrefix + "loco.build"
}

func (env environment) authURL() string {
	return "https://auth." + env.domainPrefix + "loco.build"
}

func (env environment) authIssuers() string {
	issuers := []map[string]any{{
		"issuer":   env.authURL(),
		"audience": "authenticated",
		"claims": map[string]any{
			"emailVerified": "user_metadata.email_verified",
			"name":          "user_metadata.full_name",
			"avatarUrl":     "user_metadata.avatar_url",
		},
		"web":   map[string]any{"adapter": "supabase"},
		"admin": map[string]any{"type": "supabase", "tokenEnv": "AUTH_SUPABASE_SERVICE_KEY"},
	}}
	raw, err := json.Marshal(issuers)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func api(env environment) railway.Service {
	apiEnv := preserved(
		"APP_ENV",
		"APP_PORT",
		"CACHE_ADDR",
		"CACHE_TYPE",
		"CORS_ALLOWED_ORIGINS",
		"DATABASE_URL",
		"GH_OAUTH_CLIENT_ID",
		"GH_OAUTH_CLIENT_SECRET",
		"GITLAB_PAT",
		"GITLAB_PROJECT_ID",
		"GITLAB_REGISTRY_URL",
		"GITLAB_URL",
		"LOG_LEVEL",
		"REGISTRY_TAG",
		"AUTH_SIGNUP_MODE",
		"AUTH_SIGNUP_DOMAINS",
		"AUTH_SUPABASE_SERVICE_KEY",
		hookSecret,
		"SMTP_FROM",
		"SMTP_HOST",
		"SMTP_PASSWORD",
		"SMTP_PORT",
		"SMTP_TLS",
		"SMTP_USERNAME",
	)
	apiEnv["AUTH_ISSUERS"] = env.authIssuers()
	apiEnv["WEB_URL"] = env.webURL()
	apiEnv["MIN_CLI_VERSION"] = "v0.0.61"
	apiEnv["PORT"] = "8000"
	apiEnv["RAILWAY_DEPLOYMENT_DRAINING_SECONDS"] = "40"
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

func auth(env environment, api railway.Service, db railway.Resource) railway.Service {
	authEnv := preserved(
		"GOTRUE_JWT_KEYS",
		"GOTRUE_JWT_SECRET",
		"GOTRUE_SAML_PRIVATE_KEY",
		"GOTRUE_EXTERNAL_GITHUB_CLIENT_ID",
		"GOTRUE_EXTERNAL_GITHUB_SECRET",
	)
	hooks := env.apiURL() + "/auth/hooks/"
	for name, value := range map[string]any{
		"API_EXTERNAL_URL":                        env.authURL(),
		"DATABASE_URL":                            railway.Ref(db, "DATABASE_URL"),
		"GOTRUE_API_HOST":                         "0.0.0.0",
		"GOTRUE_DB_DRIVER":                        "postgres",
		"GOTRUE_EXTERNAL_EMAIL_ENABLED":           "true",
		"GOTRUE_EXTERNAL_GITHUB_ENABLED":          "true",
		"GOTRUE_EXTERNAL_GITHUB_REDIRECT_URI":     env.authURL() + "/callback",
		"GOTRUE_HOOK_BEFORE_USER_CREATED_ENABLED": "true",
		"GOTRUE_HOOK_BEFORE_USER_CREATED_SECRETS": api.Env(hookSecret),
		"GOTRUE_HOOK_BEFORE_USER_CREATED_URI":     hooks + "before-user-created",
		"GOTRUE_HOOK_SEND_EMAIL_ENABLED":          "true",
		"GOTRUE_HOOK_SEND_EMAIL_SECRETS":          api.Env(hookSecret),
		"GOTRUE_HOOK_SEND_EMAIL_URI":              hooks + "send-email",
		"GOTRUE_JWT_ADMIN_ROLES":                  "service_role",
		"GOTRUE_JWT_AUD":                          "authenticated",
		"GOTRUE_JWT_DEFAULT_GROUP_NAME":           "authenticated",
		"GOTRUE_JWT_EXP":                          accessTokenSeconds,
		"GOTRUE_JWT_ISSUER":                       env.authURL(),
		"GOTRUE_MAILER_AUTOCONFIRM":               "false",
		"GOTRUE_SAML_ENABLED":                     "true",
		"GOTRUE_SITE_URL":                         env.webURL(),
		"GOTRUE_URI_ALLOW_LIST":                   env.webURL() + "/**",
		"PORT":                                    strconv.Itoa(authPort),
		"RAILWAY_DEPLOYMENT_DRAINING_SECONDS":     "40",
	} {
		authEnv[name] = value
	}
	deploy := limits(0.5, 500000000)
	deploy["healthcheckPath"] = "/health"
	deploy["healthcheckTimeout"] = 300
	return railway.ServiceNamed("loco::cp-auth", railway.ServiceConfig{
		"source":   image("loco-supabase-auth", "sha-"+env.commit),
		"build":    dockerBuild("/images/supabase-auth/Dockerfile"),
		"replicas": map[string]any{region: 1},
		"deploy":   deploy,
		"networking": map[string]any{
			"privateNetworkEndpoint": "auth",
			"customDomains": map[string]any{
				"auth." + env.domainPrefix + "loco.build": map[string]any{"port": authPort},
			},
		},
		"env": authEnv,
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
	apiService := api(env)
	authDB := railway.Postgres("loco::cp-auth-db", map[string]any{"region": region})
	resources := []any{
		ui(env), apiService, auth(env, apiService, authDB), authDB, cache(cacheData), cacheData, dbData,
	}
	if ctx.IsEnvironment("staging") {
		resources = append(resources, railway.Postgres("loco::cp-db-staging", map[string]any{"region": region}))
	} else {
		resources = append(resources, productionDB(dbData))
	}
	return railway.ProjectNamed("loco", resources)
}
