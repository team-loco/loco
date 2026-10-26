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

## Installation inputs

Configure the API's database, cache, authentication, registry, and API/dashboard endpoints using the repository's [environment template](https://github.com/team-loco/loco/blob/main/.env.example). Check the template and deployed release together; authentication and registry changes are under active review. Choose a [secrets key provider](secrets-key-provider.md) for the key that protects environment secrets.

Set `DATABASE_LISTEN_URL` to a direct PostgreSQL or session-pooler URL when `DATABASE_URL` uses a transaction-mode pooler. The API holds this connection for placement notifications and uses `DATABASE_URL` for ordinary queries. Both URLs must reach the same database. With `DATABASE_LISTEN_URL` empty, the listener uses `DATABASE_URL`.

Each worker cluster needs Cilium, Envoy Gateway, cert-manager, the Loco controller and the OpenTelemetry collectors from the `loco-obs` chart. The collectors export every log, trace and metric over OTLP/HTTP to one endpoint and store nothing in the cluster. The control plane cluster needs Envoy Gateway and cert-manager, and runs the API, dashboard, observability proxy and ClickHouse from the `loco-control-plane` chart.

As a [system admin](#first-system-admin), attach each cluster by creating its record with `locoadm cluster create` and passing the printed join token to the agent's first start; see [cluster attachment](../reference/cluster-attachment.md). Set the platform domains a cluster serves in `global.domain.apps` of its `loco-core` values and point `*.<domain>` at its gateway; the API stores no domains of its own.

The `loco-control-plane` chart points the proxy at its own API Service; a standalone proxy reads the API URL from `CONTROL_PLANE_URL`.

The observability proxy reports ready once its ClickHouse migrations complete and a database ping succeeds.

Set `obsProxy.enabled: false` to install the control plane without the proxy. The API then answers observability requests with `Unavailable`, and the dashboard's logs, traces and metrics show their error state. The chart runs the query role as the `obs-proxy-query` Deployment, configured under `obsProxy.query`. Set `obsProxy.ingest.enabled: true` to run OTLP ingest as the `obs-proxy-ingest` Deployment of the same image, configured under `obsProxy.ingest` and scaled by `obsProxy.ingest.replicaCount`, with the `loco_ingest` URL in the Secret named by `obsProxy.ingest.clickhouse.existingSecret`. Its Service serves OTLP/gRPC on `obsProxy.ingest.grpcPort` (4317) and OTLP/HTTP on `obsProxy.ingest.httpPort` (4318) to pods in the control-plane namespace only. A standalone proxy enables the roles with `QUERY_ENABLED` and `INGEST_ENABLED` and reads `CLICKHOUSE_INGEST_URL`, `OTLP_GRPC_PORT`, `OTLP_HTTP_PORT`, `OTLP_MAX_REQUEST_BYTES` (4 MiB), `CLICKHOUSE_INGEST_TIMEOUT` (5s) and `CLICKHOUSE_INGEST_DIAL_TIMEOUT` (10s). Ingest pods report ready once the query role's migrations have created the tables.

Set `global.exporter.endpoint` in the `loco-obs` values to the OTLP/HTTP base URL the collectors export to; the exporter appends `/v1/logs`, `/v1/traces` and `/v1/metrics`, and the chart fails to render without it. For Loco's own ingest, that is `https://<obsProxy.ingest.hostname>`. `global.exporter.headers` adds request headers. `global.exporter.bearerToken.enabled: true` sends the `token` key of the Secret named by `global.exporter.bearerToken.secretName` (`loco-otlp-exporter`) as a bearer token from both collector namespaces; `loco-core` takes the same value. Each collector queues unsent batches on disk, up to `global.exporter.queue.queue_size`, and retries without a time limit: the node collector in a hostPath directory, the two StatefulSet collectors on a PersistentVolumeClaim per replica. Telemetry survives an unreachable endpoint and collector pod replacement. Any OTLP/HTTP backend can replace the proxy.

`loco-control-plane` creates its own Gateway by default: a GatewayClass named `gateway.className`, an EnvoyProxy that takes Envoy Service settings from `gateway.service`, and a Gateway named `gateway.name` in `gatewayNamespace` with one listener for each of `ui.hostname`, `controlPlaneHostname` and, when published, `obsProxy.ingest.hostname`. Listeners serve HTTPS on port 443 with a certificate cert-manager issues for those hostnames, configured by `certManager.*` as in `loco-core`; `gateway.tlsSecretName` serves your own Secret instead, and `certManager.enabled: false` serves HTTP on port 80. Set `gateway.create: false` and `gateway.name` to attach the routes to an existing Gateway instead. The chart publishes ingest at `obsProxy.ingest.hostname` only when `obsProxy.ingest.enabled` is true and the hostname is set. Ingest does not authenticate requests yet, so the hostname is empty by default.

The proxy authenticates each query's permission check with the query's bearer token. Signed OIDC sessions and Loco API or session tokens use the API's authentication path and require read access to the requested workspace. Permission results use the proxy's configured cache lifetime.

Set `obsProxy.query.cors.allowedOrigins` to the dashboard origins that may call the proxy, such as `https://loco.build`; a standalone proxy reads them as a comma-separated list from `CORS_ALLOWED_ORIGINS`. Origins must use HTTP or HTTPS and contain no credentials, path, query or fragment. An empty list disables cross-origin browser access. The proxy answers Connect preflights and exposes protocol response headers for configured origins; requests still require a bearer token and workspace permissions.

With `clickhouse.enabled: true`, `loco-control-plane` runs ClickHouse in its namespace with three users, each limited to the `loco_obs` database: `loco_migrator` creates the schema for the observability proxy, `loco_ingest` inserts for the ingest role, and `loco_reader` serves the proxy's queries. Set `clickhouseUserPasswords.<user>` for each; the chart renders a Secret per user, named in `clickhouse.clickhouse.users`, with the password and the connection URL the proxy reads. URLs embed the passwords, so they may contain only letters, digits and `._~-`. Only the proxy pods and the ClickHouse operator can reach the server. `clickhouse.enabled` requires `obsProxy.enabled`.

Set `global.region` in the `loco-obs` values to the region the cluster is registered under in the API. The collectors stamp it on every log, trace and metric as `loco.region`, and the dashboard's per-region metrics filter on it; the chart fails to render without it. Production Helmfile reads it from `LOCO_REGION`.

ClickHouse is off by default. To use an external server such as ClickHouse Cloud, leave `clickhouse.enabled: false`, create the three users with the grants listed in `clickhouse.clickhouse.users`, and store their connection URLs in Secrets named in `obsProxy.query.clickhouse.existingSecret`, `obsProxy.query.clickhouse.migratorExistingSecret` and `obsProxy.ingest.clickhouse.existingSecret`; a standalone proxy reads the database name from `CLICKHOUSE_DB` and the URLs from `CLICKHOUSE_URL`, `CLICKHOUSE_MIGRATOR_URL` and `CLICKHOUSE_INGEST_URL`. Keep `clickhouse.clickhouse.database` equal to the database in those grants. ClickHouse Cloud serves the native protocol over TLS on port `9440`. `clickhouse.clickhouse.extraConfig`, `keeperConfig`, `settings` and `resources` apply only to the in-cluster server.

The proxy serializes its migrations through a KeeperMap table, so the ClickHouse server needs ClickHouse Keeper and a `keeper_map_path_prefix`. The in-cluster server runs a single-node Keeper from `clickhouse.clickhouse.keeperConfig`. An external server must provide both, and `loco_migrator` must be allowed to create KeeperMap tables.

Your team supplies DNS records, certificate issuance, persistent storage, backups, and capacity. Keep credentials separate across installations and environments. Use [architecture](../reference/architecture.md) to identify the control-plane and regional boundaries.

## First system admin

Operator commands in `locoadm` require the `system:admin` grant. A new installation grants it once, to a signed-in user who presents the installation's bootstrap token.

1. Generate a token of at least 32 characters, store it in a Secret, and name it in the `loco-control-plane` values. A standalone API reads it from `LOCO_BOOTSTRAP_TOKEN`.

   ```sh
   kubectl -n loco-system create secret generic loco-bootstrap \
     --from-literal=token="$(openssl rand -hex 32)"
   ```

   ```yaml
   api:
     bootstrapToken:
       existingSecret:
         name: loco-bootstrap
         key: token
   ```

2. Sign in as the future admin and present the token:

   ```sh
   loco login --host https://api.example.com
   locoadm bootstrap --token "$(kubectl -n loco-system get secret loco-bootstrap -o jsonpath='{.data.token}' | base64 -d)"
   ```

3. Remove `api.bootstrapToken` from the values, upgrade the release and delete the Secret.

The first successful bootstrap closes bootstrap permanently in the database; the token cannot reopen it after a restart. Bootstrap fails when any system admin already exists.

## Upgrades

Every commit to `main` publishes the agent, builder, controller, API, observability proxy and UI images tagged `sha-<commit>`, and each binary reports that tag as its version. The charts have no default tag for these images: set `agent.image.tag`, `controller.image.tag`, `builds.builderImage.tag` and, in `loco-control-plane`, `obsProxy.image.tag` to the same `sha-<commit>`. No chart installs CRDs. Before installing or upgrading a chart, apply `controller/config/crd/bases/*.yaml` and the Gateway API CRDs from the `gateway-helm` chart `loco-core` pins with `kubectl apply --server-side`; the platform release bundle applies them in its `crds` layer, and `mise run crds:apply` does it for a local kind cluster. Regenerate CRDs through `mise run controller:gen` when developing schema changes. Review database migrations and infrastructure changes before upgrading a running installation.

A supported production packaging and upgrade procedure is not established by these docs. Inspect the chart values and the release's configuration before deployment.
