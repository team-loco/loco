# Self-hosting

A self-hosted Loco installation puts platform operation under your team's control. The repository contains the platform components and Helm charts; production operation requires instance-specific configuration.

Start by running the [local development environment](../contributing/development.md) to exercise the components together:

```sh
mise run setup
mise run tilt
```

The local environment is a development installation. Review the repository's environment configuration before adapting it to a production cluster.

## Platform components

| Component | Repository location | Responsibility |
| --- | --- | --- |
| API | `api/` | ConnectRPC services and PostgreSQL state |
| Dashboard | `web/` | Browser access to the API |
| Regional agent | `agent/` | Connect the region to the control plane |
| Application controller | `controller/` | Reconcile Application custom resources |
| Custom resource definitions | `k8sapi/` | Kubernetes API types |
| Platform charts | `charts/` | Kubernetes platform configuration |
| Observability proxy | `observability-proxy/` | Workspace-scoped reads from ClickHouse, on the control plane |

The development CLI also plans and applies declarative [installation files](../reference/installation.md). This unreleased contract targets existing Kubernetes infrastructure and references external credentials through Secrets.

## Installation inputs

Configure the API's database, cache, authentication, registry, and API/dashboard endpoints through its environment variables. The [`loco-control-plane` chart README](https://github.com/team-loco/loco/blob/main/charts/loco-control-plane/README.md) lists the ones an installation needs, and [`api/config.go`](https://github.com/team-loco/loco/blob/main/api/config.go) reads every variable with its default. Check them against the deployed release; authentication and registry changes are under active review. The chart runs the region's OpenBao for the [secrets key provider](secrets-key-provider.md#openbao-in-the-control-plane-chart); create its seal key Secret before the first install.

Set `DATABASE_LISTEN_URL` to a direct PostgreSQL or session-pooler URL when `DATABASE_URL` uses a transaction-mode pooler. The API holds this connection for placement notifications and uses `DATABASE_URL` for ordinary queries. Both URLs must reach the same database. With `DATABASE_LISTEN_URL` empty, the listener uses `DATABASE_URL`.

Set `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` to export the API's own traces over OTLP/HTTP; with neither set, the API records no traces. The exporter reads the other standard `OTEL_EXPORTER_OTLP_*` variables, such as `OTEL_EXPORTER_OTLP_HEADERS`. `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` set the resource; without them `service.name` is `loco-api`, and `service.version` is the API's version. The API continues the W3C Trace Context (`traceparent`) of incoming requests and records a span for every HTTP request and RPC except `/`, `/health` and the agent's `Sync` stream. Its calls to PostgreSQL, Valkey, OpenBao, the source bucket, image registries, the authentication issuers, the invitation email hook and the observability proxy record child spans, and those made over HTTP send `traceparent`. Each webhook delivery attempt, sweep, event retention run and OpenBao token refresh starts its own trace. An event stores the `traceparent` of the request that recorded it, and each delivery of that event links to it. Install-wide webhooks (`INSTALL_WEBHOOKS`) receive the delivery's `traceparent`; workspace webhooks receive no trace headers. The API keeps every trace it starts except one whose first span is an outgoing call, and ignores `OTEL_TRACES_SAMPLER`. Log lines written inside a span carry its `trace_id` and `span_id`.

Set `ORG_LINKS` to add links to tools outside Loco, such as a support desk or a status page, to the Manage section of the dashboard sidebar. The value is a JSON array of at most 10 objects, each with a unique `label` of at most 32 characters and an absolute HTTP or HTTPS `url`. The dashboard replaces `{orgId}` in the URL with the current organization's ID and opens the link in a new tab; no other placeholder is allowed. The API refuses to start when the value is invalid, and an empty value shows no links. With the `loco-control-plane` chart, set it under `api.env`:

```yaml
api:
  env:
    ORG_LINKS: '[{"label":"Support","url":"https://support.example.com/orgs/{orgId}"}]'
```

With `LOCO_REGISTRY_AUTH=token` the API is the registry's [token endpoint](../reference/architecture.md#registry-access) at `/registry/token`, signing with the key the [secrets key provider](secrets-key-provider.md#configure-the-api) settings name. It reads these settings and refuses to start when one is invalid:

| Variable | Default | Meaning |
| --- | --- | --- |
| `LOCO_REGISTRY_TOKEN_SERVICE` | required for `token` | Name of the registry service; the token endpoint requires it as `service` and sets it as `aud`. It must match the registry's bearer `service`. The chart sets it from `api.registry.service` |
| `LOCO_REGISTRY_ACCESS_TOKEN_LIFETIME` | `5m` | Lifetime of a registry access token, positive |
| `LOCO_REGISTRY_PULL_CREDENTIAL_LIFETIME` | `24h` | Lifetime of a workspace pull credential issued by `RegistryService.IssuePullCredential`, positive |
| `LOCO_REGISTRY_CREDENTIAL_SWEEP_INTERVAL` | `10m` | How often the API deletes expired pull credentials and build credentials whose build is no longer running on their cluster, positive |

Each worker cluster needs a CNI that enforces Kubernetes NetworkPolicy, cert-manager and the `loco-worker` chart, which runs the agent, the Loco controllers, Envoy Gateway and the OpenTelemetry collectors. Create its release namespace `loco-system` with the Pod Security labels in `manifests/namespaces/loco-system.yaml` before installing it. The collectors export every log, trace and metric over OTLP/HTTP to one endpoint and store nothing in the cluster. The control plane cluster needs Envoy Gateway and cert-manager, and runs the API, dashboard, observability proxy, ClickHouse and OpenBao from the `loco-control-plane` chart.

As a [system admin](#first-system-admin), attach each cluster by creating its record with `locoadm cluster create` and passing the printed join token to the agent's first start; see [cluster attachment](../reference/cluster-attachment.md). Set the platform domains a cluster serves in `global.domain.apps` of its `loco-worker` values and point `*.<domain>` at its gateway; the API stores no domains of its own.

Envoy Gateway must use `config.envoyGateway.provider.kubernetes.deploy.type: GatewayNamespace`. The worker chart enables this mode so proxies run in their Gateway's namespace, as application ingress policies require. A shared controller can serve the control plane and worker without moving worker proxies into the controller namespace.

The `loco-control-plane` chart points the proxy at its own API Service; a standalone proxy reads the API URL from `CONTROL_PLANE_URL`.

The observability proxy reports ready once its ClickHouse migrations complete and a database ping succeeds.

Set `obsProxy.enabled: false` to install the control plane without the proxy. The API then answers observability requests with `Unavailable`, and the dashboard's logs, traces and metrics show their error state. The chart runs the query role as the `obs-proxy-query` Deployment, configured under `obsProxy.query`. Set `obsProxy.ingest.enabled: true` to run OTLP ingest as the `obs-proxy-ingest` Deployment of the same image, configured under `obsProxy.ingest` and scaled by `obsProxy.ingest.replicaCount`, with the `loco_ingest` URL in the Secret named by `obsProxy.ingest.clickhouse.existingSecret`. Its Service serves OTLP/gRPC on `obsProxy.ingest.grpcPort` (4317) and OTLP/HTTP on `obsProxy.ingest.httpPort` (4318) to pods in the control-plane namespace only. A standalone proxy enables the roles with `QUERY_ENABLED` and `INGEST_ENABLED` and reads `CLICKHOUSE_INGEST_URL`, `OTLP_GRPC_PORT`, `OTLP_HTTP_PORT`, `OTLP_MAX_REQUEST_BYTES` (4 MiB), `CLICKHOUSE_INGEST_TIMEOUT` (5s) and `CLICKHOUSE_INGEST_DIAL_TIMEOUT` (10s). Ingest pods report ready once the query role's migrations have created the tables.

Set `global.exporter.endpoint` in the `loco-worker` values to the OTLP/HTTP base URL the collectors export to; the exporter appends `/v1/logs`, `/v1/traces` and `/v1/metrics`, and the chart fails to render without it. For Loco's own ingest, that is `https://<obsProxy.ingest.hostname>`. `global.exporter.headers` adds request headers. `global.exporter.ingestToken.enabled: true` sends the `token` key of the Secret named by `global.exporter.ingestToken.secretName` (`loco-otlp-exporter`) as a bearer token from both collector namespaces, `loco-obs` and `loco-obs-node`. Each collector persists requests on disk before batching, up to `global.exporter.queue.queue_size`, and retries without a time limit: the node collector in a hostPath directory, the two StatefulSet collectors on a PersistentVolumeClaim per replica. Batching uses `global.exporter.queue.batch`: 100 items or a 1s flush timeout by default. A full queue blocks senders (`block_on_overflow: true`). Telemetry survives an unreachable endpoint and collector pod replacement. Any OTLP/HTTP backend can replace the proxy.

`loco-control-plane` creates its own Gateway by default: a GatewayClass named `gateway.className`, an EnvoyProxy that takes Envoy Service settings from `gateway.service`, and a Gateway named `gateway.name` in `gatewayNamespace` with one listener for each of `ui.hostname`, `controlPlaneHostname` and, when published, `obsProxy.ingest.hostname`. Listeners serve HTTPS on port 443 with a certificate cert-manager issues for those hostnames, configured by `certManager.*` as in `loco-worker` (`certManager.issuer.solvers` takes any cert-manager solver list, Cloudflare DNS-01 by default); `gateway.tlsSecretName` serves your own Secret instead, and `certManager.enabled: false` serves HTTP on port 80. Set `gateway.create: false` and `gateway.name` to attach the routes to an existing Gateway instead. The chart publishes ingest at `obsProxy.ingest.hostname` only when `obsProxy.ingest.enabled` is true and the hostname is set. The hostname has no default.
Ingest accepts only clusters with an ingest token from the API; see [architecture](../reference/architecture.md#observability). The API signs tokens with the OpenBao Transit key `openbao.transit.ingestKey`, which the setup Job creates; [rotate it](secrets-key-provider.md#rotation) in OpenBao. Set `global.exporter.ingestToken.enabled: true` in the `loco-worker` values to send the token; `dev/worker-chart.yaml` sets it for the local stack. The agent then writes its token to the Secret and key named by `global.exporter.ingestToken.secretName` and `secretKey`, the Secret the collectors' `bearertokenauth` extension reads, in `loco-obs` and `loco-obs-node`; the chart lets it create Secrets there and `get` and `update` only that one. Every collector pod runs an `exporter-token-wait` init container that polls every `global.exporter.ingestToken.waitIntervalSeconds` (2) until the token file exists and is not empty, because a collector authenticates only if the file is present when it starts.
The ingest proxy reads the API from `CONTROL_PLANE_URL`, which the chart sets, and accepts records up to `INGEST_CLOCK_SKEW` (5m) outside a placement window; raise it when worker clocks drift further. A standalone ingest proxy also reads `INGEST_AUTH_TIMEOUT` (5s), `INGEST_JWKS_REFRESH_INTERVAL` (30s), `INGEST_PLACEMENT_CACHE_TTL` (30s), `INGEST_PLACEMENT_REFETCH_INTERVAL` (5s) and `INGEST_RETRY_AFTER` (30s).

The proxy authenticates each query's permission check with the query's bearer token. Signed OIDC sessions and Loco API or session tokens use the API's authentication path and require read access to the requested workspace. Permission results use the proxy's configured cache lifetime.

Set `obsProxy.query.cors.allowedOrigins` to the dashboard origins that may call the proxy, such as `https://loco.build`; a standalone proxy reads them as a comma-separated list from `CORS_ALLOWED_ORIGINS`. Origins must use HTTP or HTTPS and contain no credentials, path, query or fragment. An empty list disables cross-origin browser access. The proxy answers Connect preflights and exposes protocol response headers for configured origins; requests still require a bearer token and workspace permissions.

With `clickhouse.enabled: true`, `loco-control-plane` runs ClickHouse in its namespace with three users, each limited to the `loco_obs` database: `loco_migrator` creates the schema for the observability proxy, `loco_ingest` inserts for the ingest role, and `loco_reader` serves the proxy's queries. Create each Secret named by `clickhouse.clickhouse.users[].password_secret_name` before installing, with a `password` key and a `url` key containing `clickhouse://USER:PASSWORD@clickhouse-CHI_NAME.NAMESPACE.svc.cluster.local:9000`. `CHI_NAME` is `clickhouse.fullnameOverride`, which defaults to `loco-clickhouse` so generated operator volume names fit Kubernetes limits. URL-encode the password in the URL. The chart consumes these existing Secrets; credentials must stay out of Helm values and release history. Only the proxy pods and the ClickHouse operator can reach the server. `clickhouse.enabled` requires `obsProxy.enabled`.

The ClickHouse operator also reads an existing Secret named by `clickhouse.operator.fullnameOverride`. Its `username` matches `clickhouse.operator.secret.username`; its `password` is generated outside Helm. The chart disables operator credential creation, and `locoadm apply` creates and preserves the native Secret.

Set `global.region` in the `loco-worker` values to the region the cluster is registered under in the API. The collectors stamp it on every log, trace and metric as `loco.region`, and the dashboard's per-region metrics filter on it; the chart fails to render without it.

ClickHouse is off by default. To use an external server such as ClickHouse Cloud, leave `clickhouse.enabled: false`, create the three users with the grants listed in `clickhouse.clickhouse.users`, and store their connection URLs in Secrets named in `obsProxy.query.clickhouse.existingSecret`, `obsProxy.query.clickhouse.migratorExistingSecret` and `obsProxy.ingest.clickhouse.existingSecret`; a standalone proxy reads the database name from `CLICKHOUSE_DB` and the URLs from `CLICKHOUSE_URL`, `CLICKHOUSE_MIGRATOR_URL` and `CLICKHOUSE_INGEST_URL`. Keep `clickhouse.clickhouse.database` equal to the database in those grants. ClickHouse Cloud serves the native protocol over TLS on port `9440`. `clickhouse.clickhouse.extraConfig`, `keeperConfig`, `settings` and `resources` apply only to the in-cluster server. To keep ingest inserts out of `system.query_log`, set `log_queries = 0` in the `loco_ingest` user's settings profile; the proxy does not set it.

The proxy serializes its migrations through a KeeperMap table, so the ClickHouse server needs ClickHouse Keeper and a `keeper_map_path_prefix`. The in-cluster server runs a single-node Keeper from `clickhouse.clickhouse.keeperConfig`. An external server must provide both, and `loco_migrator` must be allowed to create KeeperMap tables.

Your team supplies DNS records, certificate issuance, persistent storage, backups, and capacity. Keep credentials separate across installations and environments. Use [architecture](../reference/architecture.md) to identify the control-plane and regional boundaries.

## First system admin

Operator commands in `locoadm` require the `system:admin` grant. `BootstrapService.Bootstrap` requires both the installation's bootstrap token and an identity-provider JWT in the Authorization header. The API verifies the issuer, signature, audience, expiry and verified email before resolving or creating the first administrator, including when ordinary signup is closed. It returns a normal CLI session.

Bootstrap creates the user, grants system administration and closes bootstrap in one transaction. Concurrent attempts produce one administrator. A restart, a lost response or removal of an administrator never reopens bootstrap. The unreleased [installation workflow](../reference/installation.md#apply-and-resume) uses `locoadm bootstrap --file installation.yaml --email admin@example.com` to sign in, save a CLI session, remove the temporary bootstrap reference from every API replica, and delete its Secret. Repeat the command as an existing system administrator to resume interrupted cleanup.

## Upgrades

Every commit to `main` publishes the agent, builder, controller, API, observability proxy and UI images tagged `sha-<commit>`, and each binary reports that tag as its version. The charts have no default tag for these images: set `agent.image.tag`, `controller.image.tag`, `builds.builderImage.tag` and, in `loco-control-plane`, `obsProxy.image.tag` to the same `sha-<commit>`. Each chart ships a `values.schema.json`, so Helm rejects a missing image tag, an unknown key or a value of the wrong type before it renders anything. No chart installs CRDs. Before installing or upgrading a chart, apply `controller/config/crd/bases/*.yaml` and the Gateway API CRDs from the `gateway-helm` chart `loco-worker` pins with `kubectl apply --server-side`; the platform release bundle applies them in its `crds` layer, and `mise run crds:apply` does it for a local kind cluster. Regenerate CRDs through `mise run controller:gen` when developing schema changes. Each chart and CRD bundle carries its own version, in its `Chart.yaml` or `tools/platform-release/crds.yaml`, and a push to `main` publishes every version not yet published; `tools/platform-release/README.md` describes the artifacts and the platform bundle that pins them. Review database migrations and infrastructure changes before upgrading a running installation.

A supported production packaging and upgrade procedure is not established by these docs. Inspect the chart values and the release's configuration before deployment.
