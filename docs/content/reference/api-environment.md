---
title: "API environment"
---

The API reads its configuration from environment variables at startup. This page lists every variable it reads, with its default and effect. An invalid value stops the API at startup with an error naming the variable. Durations are Go durations such as `30s`, `5m` or `168h`.

The `loco-control-plane` chart sets some of these from typed values and passes the rest from `api.env` or the API environment Secret; its [README](https://github.com/team-loco/loco/blob/main/charts/loco-control-plane/README.md) names which. `api/config.go` holds the defaults.

## Server

| Variable | Default | Effect |
| --- | --- | --- |
| `APP_ENV` | empty | `PRODUCTION` writes JSON logs to stdout at `LOG_LEVEL` and refuses CORS requests from loopback origins. Any other value writes human-readable logs to stderr. |
| `APP_PORT` | empty | Listen address, such as `:8000`. Empty listens on port 80. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`, for the JSON logs of `APP_ENV=PRODUCTION`. |
| `GOMEMLIMIT` | none | Go runtime soft memory limit, such as `900MiB`. The chart sets it to the container memory limit. `GOMAXPROCS` follows the container CPU limit, at least 2, without being set. |
| `PPROF_ADDR` | empty | Address of a Go pprof server. Empty runs none. |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated browser origins allowed to call the API. The `WEB_URL` origin is always allowed. |
| `WEB_URL` | empty | Dashboard origin. Required when `AUTH_ISSUERS` configures a dashboard client; invitation links point at it. |
| `LOCO_TRUSTED_PROXY_HOPS` | `0` | How many proxies in front of the API append the address they received a request from to `X-Forwarded-For`. The API shows the entry that many places from the right as a session's address; `0` uses the connection's address. The chart sets `1` for its gateway. |
| `LOCO_RPC_TIMEOUT` | `30s` | Deadline of each unary RPC. Streams have none. |
| `LOCO_SHUTDOWN_DRAIN_TIMEOUT` | `30s` | How long shutdown waits for open requests before closing their connections. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | OTLP/HTTP endpoint for the API's own traces and Go runtime metrics. Tracing is off while this and `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` are both empty; metrics are off while this and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` are both empty. A backend that accepts only traces belongs in `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, which leaves metrics off. The OpenTelemetry SDK reads its other `OTEL_*` variables too. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | empty | OTLP/HTTP endpoint for traces only. |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | empty | OTLP/HTTP endpoint for metrics only. The API exports Go runtime metrics: memory, garbage collection goal, goroutine count and scheduler latency (`go.schedule.duration`). |
| `OTEL_PROPAGATORS` | `tracecontext,baggage` | Trace context formats the API reads from requests and writes to outgoing calls: `tracecontext`, `baggage`, `b3`, `b3multi`, `jaeger`, `xray`, `ottrace` or `none`. Each request starts its own trace and links to the caller's span, so a client cannot choose the trace a request joins or whether it is sampled. |

## Database

| Variable | Default | Effect |
| --- | --- | --- |
| `DATABASE_URL` | required | PostgreSQL connection string. Startup migrations run over it, so use a direct connection. |
| `DATABASE_LISTEN_URL` | `DATABASE_URL` | Connection for `LISTEN` notifications. Set it to a direct connection when `DATABASE_URL` goes through a transaction pooler. |
| `DATABASE_MAX_CONNS` | `25` | Largest connection pool size. |
| `DATABASE_MIN_CONNS` | `20` | Connections the pool keeps open, so a burst of queries from a page load finds them ready. Must not exceed `DATABASE_MAX_CONNS`; unset, it is 20 or `DATABASE_MAX_CONNS`, whichever is smaller. Each API replica holds this many PostgreSQL connections. |
| `DATABASE_STATEMENT_TIMEOUT` | `15s` | PostgreSQL `statement_timeout` of each pooled connection. |
| `LOCO_PLACEMENT_POLL_INTERVAL` | `5s` | How often agent streams recheck placements in case a notification was missed. |
| `LOCO_ACTIVITY_POLL_INTERVAL` | `30s` | How often deployment watches recheck activity in case a notification was missed. |

## Sign-in and tokens

| Variable | Default | Effect |
| --- | --- | --- |
| `AUTH_ISSUERS` | empty | JSON array of OpenID Connect issuers, each with `issuer`, `audience` and an optional `web` dashboard client. See [installation](installation.md#register-the-dashboard-with-your-identity-provider). |
| `AUTH_SIGNUP_MODE` | `open` | `closed` refuses sign-in to anyone without an account. |
| `LOCO_SYSTEM_ADMINS` | empty | Comma-separated emails that become system admins on their first verified sign-in. See [installation roles](../operations/self-hosting.md#installation-roles). |
| `LOCO_SYSTEM_READERS` | empty | Comma-separated emails that become system readers on their first verified sign-in. An email cannot appear in both lists. |
| `INVITATION_TTL` | `168h` | How long an organization invitation stays open. |
| `LOCO_API_TOKEN_MAX_LIFETIME` | `8760h` | Longest lifetime of an API token, at least one second. See [API token lifetime](../operations/access.md#api-token-lifetime). |
| `LOCO_SESSION_ACCESS_TOKEN_LIFETIME` | `1h` | Lifetime of a session access token. |
| `LOCO_SESSION_REFRESH_TOKEN_LIFETIME` | `168h` | Lifetime of a session refresh token, and so of a session nobody uses. |
| `LOCO_SESSION_REFRESH_REUSE_GRACE` | `10s` | How long after a session refresh the old refresh token still counts as a concurrent refresh, such as a second browser tab, and keeps the session. Later reuse revokes the session. |
| `LOCO_TOKEN_LAST_USED_INTERVAL` | `5m` | Least time between two writes of a token's last-used time or a member's last sign-in. |
| `LOCO_TOKEN_CLEANUP_INTERVAL` | `1m` | How often the API deletes expired session tokens, API tokens, cluster assertions and sign-in state. |
| `LOCO_STREAM_AUTH_INTERVAL` | `5s` | Time between access rechecks of an open stream. See [open streams](../operations/access.md#open-streams-and-access-changes). |
| `LOCO_STREAM_AUTH_TIMEOUT` | `2s` | Limit on each stream access recheck; at most `LOCO_STREAM_AUTH_INTERVAL`. |
| `LOCO_MAX_STREAMS` | `500` | Log tails, event streams and deployment watches one API instance holds open; it refuses more with `ResourceExhausted`. The chart sets it from `api.streams.maxPerReplica`. |
| `LOCO_MAX_CALLER_STREAMS` | `50` | Streams one caller holds open on one API instance; it refuses more with `ResourceExhausted`. The chart sets it from `api.streams.maxPerCaller`. |

### Sign-ins in progress

A dashboard sign-in, `loco login` and `loco login --device` keep their state in PostgreSQL between requests, so any API replica can finish a sign-in another one started. A dashboard sign-in stays open for 5 minutes and a device sign-in for 10, until it finishes. At most 1,000 sign-ins are in progress at once; starting one more drops the oldest, which then has to start again. The table holding them is not written to the write-ahead log, so sign-ins in progress are lost if the database restarts after a crash or fails over; users start them again.

## Clusters and agents

| Variable | Default | Effect |
| --- | --- | --- |
| `LOCO_TARGET_PLATFORM` | `linux/amd64` | Processor applications are built for and run on: `linux/amd64` or `linux/arm64`. |
| `LOCO_AGENT_HEARTBEAT_INTERVAL` | `30s` | Interval agents send heartbeats at. A cluster that misses three is treated as silent. |
| `LOCO_AGENT_ASSERTION_MAX_LIFETIME` | `5m` | Longest lifetime the API accepts on an agent's signed cluster assertion. |
| `LOCO_CLUSTER_JOIN_TOKEN_TTL` | `1h` | Default lifetime of a cluster join token. See [cluster enrollment](cluster-attachment.md). |
| `LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL` | `168h` | Longest lifetime a join token may request. Must be at least `LOCO_CLUSTER_JOIN_TOKEN_TTL`. |
| `LOCO_APPLY_ATTEMPTS` | `2` | Times the API sends a deployment a cluster fails to apply before it marks the deployment failed, 1 to 10. |
| `LOCO_APPLY_RETRY_DELAY` | `300` | Seconds between those sends. |
| `LOCO_WORKSPACE_NAMESPACE_GRACE` | `24h` | How long a workspace may have no deployments on a cluster before the API leaves it off the workspaces that cluster keeps, and the cluster's agent deletes the workspace's namespaces there. |

## Builds and images

| Variable | Default | Effect |
| --- | --- | --- |
| `LOCO_SOURCE_BUCKET` | empty | S3 bucket that holds uploaded build sources. Empty turns source builds off. |
| `LOCO_SOURCE_BUCKET_ENDPOINT` | empty | S3 endpoint URL. Empty uses the AWS S3 endpoint of the region. |
| `LOCO_SOURCE_BUCKET_REGION` | empty | Bucket region. Required with a bucket. |
| `LOCO_SOURCE_BUCKET_ACCESS_KEY_ID` | empty | Access key ID. Required with a bucket. |
| `LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY` | empty | Secret access key. Required with a bucket. |
| `LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE` | `false` | Use path-style bucket URLs. |
| `LOCO_SOURCE_MAX_BYTES` | `209715200` | Largest source upload in bytes (200 MiB). |
| `LOCO_SOURCE_SWEEP_INTERVAL` | `10m` | How often the source sweeper runs. |
| `LOCO_SOURCE_SWEEP_UPLOAD_GRACE` | `1h` | Time after the upload URL expires before a build still waiting for its source fails. |
| `LOCO_SOURCE_SWEEP_BUILD_BATCH` | `100` | Builds the source sweeper expires, and finished builds whose source it deletes, per run. |
| `LOCO_SOURCE_SWEEP_ORPHAN_MIN_AGE` | `24h` | Age a bucket object with no build must reach before the sweeper deletes it. |
| `LOCO_SOURCE_SWEEP_ORPHAN_PAGE_SIZE` | `1000` | Bucket objects listed per page when looking for orphans. |
| `LOCO_SOURCE_SWEEP_ORPHAN_MAX_PAGES` | `5` | Pages of bucket objects checked per run. |
| `LOCO_PUBLIC_IMAGE_MAX_BYTES` | `1073741824` | Largest compressed size of a prebuilt public image in bytes (1 GiB). |
| `LOCO_IMAGE_RESOLVE_TIMEOUT` | `15s` | Limit on resolving a prebuilt image to its digest. |
| `LOCO_IMAGE_RETENTION` | `5` | Newest succeeded build images kept per resource. `0` turns image deletion off. |
| `LOCO_IMAGE_SWEEP_INTERVAL` | `10m` | How often the image sweeper runs. |
| `LOCO_IMAGE_SWEEP_BUILD_BATCH` | `100` | Builds whose image the sweeper deletes per run. |
| `LOCO_IMAGE_SWEEP_REPOSITORY_BATCH` | `100` | Registry repositories the sweeper checks for stale tags per run. |
| `LOCO_IMAGE_SWEEP_TAG_BATCH` | `100` | Stale tags the sweeper deletes per run. |
| `LOCO_IMAGE_SWEEP_TAG_MIN_AGE` | `1h` | Age a stale build tag must reach before the sweeper deletes it. |

## Registry

| Variable | Default | Effect |
| --- | --- | --- |
| `LOCO_REGISTRY_HOST` | empty | Registry host that builds push to and applications pull from. |
| `LOCO_REGISTRY_PREFIX` | empty | Repository path prefix under the host. |
| `LOCO_REGISTRY_URL` | `https://` and `LOCO_REGISTRY_HOST` | Base URL the image sweeper calls. |
| `LOCO_REGISTRY_USERNAME` | empty | Registry user of the image sweeper in `static` mode. Without it, build images are never deleted. |
| `LOCO_REGISTRY_PASSWORD` | empty | Password of `LOCO_REGISTRY_USERNAME`. |
| `LOCO_REGISTRY_TIMEOUT` | `30s` | Limit on each registry request of the image sweeper. |
| `LOCO_REGISTRY_AUTH` | `static` | `static` or `token`. See [registry access](architecture.md#registry-access). |
| `LOCO_REGISTRY_TRANSIT_KEY` | empty | Transit key that signs registry access tokens. Required for `token`. |
| `LOCO_REGISTRY_TRANSIT_KEY_VERSION` | empty | Version of `LOCO_REGISTRY_TRANSIT_KEY` the API signs with. Required for `token`. |
| `LOCO_REGISTRY_TOKEN_SERVICE` | empty | Registry `service` name the token endpoint accepts. Required for `token`. |
| `LOCO_REGISTRY_ACCESS_TOKEN_LIFETIME` | `5m` | Lifetime of a registry access token. |
| `LOCO_REGISTRY_PULL_CREDENTIAL_LIFETIME` | `24h` | Lifetime of a workspace pull credential. |
| `LOCO_REGISTRY_CREDENTIAL_SWEEP_INTERVAL` | `10m` | How often the API deletes expired registry credentials. |

## Secrets and signing keys

[The secrets key provider](../operations/secrets-key-provider.md#configure-the-api) describes the Transit settings in full.

| Variable | Default | Effect |
| --- | --- | --- |
| `LOCO_SECRETS_TRANSIT_ADDR` | required | Base URL of the OpenBao or Vault server. |
| `LOCO_SECRETS_TRANSIT_MOUNT` | `transit` | Mount path of the Transit secrets engine. |
| `LOCO_SECRETS_TRANSIT_KEY` | required | Transit key that wraps secret data keys. |
| `LOCO_SECRETS_TRANSIT_AUTH` | required | `kubernetes` or `token`. |
| `LOCO_SECRETS_TRANSIT_KUBERNETES_MOUNT` | empty | Kubernetes auth mount. Required for `kubernetes`. |
| `LOCO_SECRETS_TRANSIT_KUBERNETES_ROLE` | empty | Kubernetes auth role. Required for `kubernetes`. |
| `LOCO_SECRETS_TRANSIT_KUBERNETES_TOKEN_FILE` | empty | ServiceAccount token file. Required for `kubernetes`. |
| `LOCO_SECRETS_TRANSIT_TOKEN` | empty | Server token. Required for `token`. |
| `LOCO_SECRETS_TRANSIT_CA_FILE` | system roots | PEM bundle that signs the server's certificate. |
| `LOCO_SECRETS_TRANSIT_TIMEOUT` | `10s` | Limit on each request to the server. |
| `LOCO_SECRETS_TRANSIT_STARTUP_TIMEOUT` | `30s` | Limit on the first login and the key checks at startup. |
| `LOCO_SECRETS_TRANSIT_RENEW_MARGIN` | `5m` | How long before the server token expires the API renews it. |
| `LOCO_SECRETS_TRANSIT_RENEW_RETRY` | `10s` | Wait after a failed renewal or login. |
| `LOCO_SECRETS_TRANSIT_CACHE_TTL` | `5m` | How long the API keeps an unwrapped data key in memory. |
| `LOCO_SECRETS_MAX_VALUE_BYTES` | `65536` | Largest secret value in bytes. |
| `LOCO_SECRETS_MAX_PLACEMENT_BYTES` | `786432` | Largest total size of the secrets one placement delivers, in bytes. |
| `LOCO_SECRETS_LOCK_TIMEOUT` | `10s` | How long a secret write waits for the locks on its environment and its environment key. |
| `LOCO_INGEST_TRANSIT_KEY` | required | Transit key that signs ingest tokens. |
| `LOCO_INGEST_TOKEN_LIFETIME` | `1h` | Lifetime of an ingest token. |

## Observability queries

| Variable | Default | Effect |
| --- | --- | --- |
| `CLICKHOUSE_URL` | empty | Read-only ClickHouse DSN. Empty makes the observability RPCs answer `Unavailable`. |
| `CLICKHOUSE_DB` | empty | ClickHouse database. Required with `CLICKHOUSE_URL`. |
| `CLICKHOUSE_MAX_CONNS` | `16` | Largest ClickHouse connection pool size, and the most queries one API replica runs at once. |
| `CLICKHOUSE_QUERY_TIMEOUT` | `10s` | Limit on each ClickHouse query, in whole seconds. |
| `LOCO_OBSERVABILITY_DEFAULT_LIMIT` | `1000` | Rows a query returns when it sets no limit. |
| `LOCO_OBSERVABILITY_MAX_LIMIT` | `10000` | Most rows a query may request. |
| `LOCO_OBSERVABILITY_MAX_TIME_RANGE` | `24h` | Longest time range of a query. |
| `LOCO_OBSERVABILITY_MAX_BUCKETS` | `1440` | Most buckets of a bucketed query. |
| `LOCO_OBSERVABILITY_MAX_TAIL_DURATION` | `30m` | How long a log tail stays open. |

## Events and webhooks

| Variable | Default | Effect |
| --- | --- | --- |
| `EVENTS_RETENTION` | `2160h` | How long audit events are kept. |
| `INSTALL_WEBHOOKS` | empty | JSON array of install webhooks. See [webhooks](../operations/webhooks.md). |
| `NTFY_URL` | empty | Base URL of an ntfy server. With `NTFY_TOPIC`, turns on ntfy notifications. See [ntfy notifications](../operations/ntfy.md). |
| `NTFY_TOPIC` | empty | ntfy topic that receives the notifications: letters, digits, `_` and `-`, at most 64 characters. |
| `NTFY_TOKEN` | empty | Access token for a protected topic. Keep it in the API environment Secret. |
| `NTFY_EVENT_TYPES` | `build.failed,deployment.failed,deployment.crashed,cluster.silent,cluster.recovered,cluster.health_changed,org.suspended` | Comma-separated event types to notify. |
| `WEBHOOK_TIMEOUT` | `10s` | Limit on each delivery attempt. |
| `WEBHOOK_RETRY_DELAYS` | `30s,2m,10m,1h,6h,24h` | Comma-separated waits between delivery attempts. |
| `WEBHOOK_BATCH` | `50` | Deliveries the dispatcher claims at a time. |
| `WEBHOOK_POLL_INTERVAL` | `2s` | How often the dispatcher checks for deliveries. |
| `WEBHOOK_ALLOW_PRIVATE_NETWORKS` | `false` | Allow workspace webhooks to use `http` URLs and deliver to private, loopback and link-local addresses. |

## CLI and dashboard

`ConfigService.GetConfig` returns these to the CLI and the dashboard.

| Variable | Default | Effect |
| --- | --- | --- |
| `MIN_CLI_VERSION` | empty | Oldest `loco` version the API supports, such as `v0.0.61`. Older CLIs print an update hint. |
| `LOCO_SCHEMA_URL` | `https://loco.build/schemas/loco.v1.json` | loco.yaml JSON schema that `loco infra init` references. |
| `LOCO_DEFAULT_REGION` | empty | Region code new services default to, such as `us-ca-1`. Ignored while no cluster has it; see [regions](regions.md#default-region). |
| `LOCO_DEPLOYMENT_MODE` | `self-hosted` | Who runs this install: `self-hosted` (an operator runs Loco themselves), `dedicated` (a single-tenant install run by the Loco team) or `multi-tenant` (the hosted service shared by many organizations). In `multi-tenant` the dashboard shows its landing page at `/` and a Spend card on the dashboard; in the others `/` goes to sign-in. The control-plane chart value `deploymentMode` sets it. Any other value stops the API at startup. |
| `ORG_LINKS` | empty | JSON array of up to 10 `{"label", "url"}` links the dashboard sidebar shows for each organization. A URL may contain `{orgId}`. |

## Service defaults

The API fills these into a service that leaves the field out. See [loco.yaml](../deployment/loco-yaml.md).

| Variable | Default |
| --- | --- |
| `LOCO_DEFAULT_CPU` | `100m` |
| `LOCO_DEFAULT_MEMORY` | `256Mi` |
| `LOCO_DEFAULT_MIN_REPLICAS` | `1` |
| `LOCO_DEFAULT_MAX_REPLICAS` | `1` |
| `LOCO_DEFAULT_CPU_TARGET` | `70` (percent) |
| `LOCO_DEFAULT_PATH_PREFIX` | `/` |
| `LOCO_DEFAULT_IDLE_TIMEOUT` | `60` (seconds) |
| `LOCO_DEFAULT_HEALTH_PATH` | `/health` |
| `LOCO_DEFAULT_HEALTH_INTERVAL` | `30` (seconds) |
| `LOCO_DEFAULT_HEALTH_TIMEOUT` | `5` (seconds) |
| `LOCO_DEFAULT_HEALTH_FAIL_THRESHOLD` | `3` |
| `LOCO_DEFAULT_HEALTH_STARTUP_GRACE_PERIOD` | `0` (seconds) |
| `LOCO_DEFAULT_MAX_RESTARTS` | `2` (1 to 5, under the default `on-failure` restart policy) |
| `LOCO_DEFAULT_DEPLOY_TIMEOUT` | `600` (seconds, 60 to 3600) |

## Count limits

[Count limits](../operations/access.md#count-limits) describes what each limit counts.

| Variable | Default |
| --- | --- |
| `LOCO_LIMIT_ORGS_PER_USER` | `10` |
| `LOCO_LIMIT_WORKSPACES_PER_ORG` | `100` |
| `LOCO_LIMIT_MEMBERS_PER_ORG` | `500` |
| `LOCO_LIMIT_INVITATIONS_PER_ORG` | `100` |
| `LOCO_LIMIT_ENVIRONMENTS_PER_WORKSPACE` | `10` |
| `LOCO_LIMIT_RESOURCES_PER_WORKSPACE` | `100` |
| `LOCO_LIMIT_WEBHOOKS_PER_WORKSPACE` | `20` |
| `LOCO_LIMIT_TOKENS_PER_ENTITY` | `50` |
| `LOCO_LIMIT_DOMAINS_PER_RESOURCE` | `10` |
| `LOCO_LIMIT_SECRETS_PER_ENVIRONMENT` | `256` |
| `LOCO_LIMIT_DEPLOYMENT_HISTORY` | `100` |
| `LOCO_LIMIT_BUILD_HISTORY` | `100` |
| `LOCO_LIMIT_LOG_RETENTION_DAYS` | `365` |
| `LOCO_LIMIT_TRACE_RETENTION_DAYS` | `365` |
| `LOCO_LIMIT_METRIC_RETENTION_DAYS` | `365` |
| `LOCO_LIMIT_EXPIRED_TOKEN_RETENTION_DAYS` | `30` |
| `LOCO_LIMIT_SERVICES_PER_FILE` | `100` |
