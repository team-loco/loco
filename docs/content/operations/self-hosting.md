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

The cluster needs Cilium, Envoy Gateway, cert-manager, and the Loco controller. Each cluster runs the OpenTelemetry collectors and, by default, ClickHouse from the `loco-obs` chart. The observability proxy runs on the control plane. `charts/loco-control-plane` holds its Kubernetes manifests; that chart is a work in progress, and no Helmfile release, platform bundle or published release ships it. The charts and environment values define their configuration.

Attach each cluster by creating its record with `locoadm cluster create` and passing the printed join token to the agent's first start; see [cluster attachment](../reference/cluster-attachment.md).

The `loco-control-plane` chart points the proxy at its own API Service; a standalone proxy reads the API URL from `CONTROL_PLANE_URL`.

The observability proxy reports ready once its ClickHouse migrations complete and a database ping succeeds.

Set `obsProxy.enabled: false` to install the control plane without the proxy. The API then answers observability requests with `Unavailable`, and the dashboard's logs, traces and metrics show their error state. The chart runs the query role as the `obs-proxy-query` Deployment, configured under `obsProxy.query`. Set `obsProxy.ingest.enabled: true` to run OTLP ingest as the `obs-proxy-ingest` Deployment of the same image, configured under `obsProxy.ingest` and scaled by `obsProxy.ingest.replicaCount`, with the `loco_ingest` URL in the Secret named by `obsProxy.ingest.clickhouse.existingSecret`. Its Service serves OTLP/gRPC on `obsProxy.ingest.grpcPort` (4317) and OTLP/HTTP on `obsProxy.ingest.httpPort` (4318) to pods in the control-plane namespace only. A standalone proxy enables the roles with `QUERY_ENABLED` and `INGEST_ENABLED` and reads `CLICKHOUSE_INGEST_URL`, `OTLP_GRPC_PORT`, `OTLP_HTTP_PORT`, `OTLP_MAX_REQUEST_BYTES` (4 MiB), `CLICKHOUSE_INGEST_TIMEOUT` (5s) and `CLICKHOUSE_INGEST_DIAL_TIMEOUT` (10s). Ingest pods report ready once the query role's migrations have created the tables.

The proxy authenticates each query's permission check with the query's bearer token. Signed OIDC sessions and Loco API or session tokens use the API's authentication path and require read access to the requested workspace. Permission results use the proxy's configured cache lifetime.

Set `obsProxy.query.cors.allowedOrigins` to the dashboard origins that may call the proxy, such as `https://loco.build`; a standalone proxy reads them as a comma-separated list from `CORS_ALLOWED_ORIGINS`. Origins must use HTTP or HTTPS and contain no credentials, path, query or fragment. An empty list disables cross-origin browser access. The proxy answers Connect preflights and exposes protocol response headers for configured origins; requests still require a bearer token and workspace permissions.

The `loco-obs` chart defines three ClickHouse users, each limited to the `loco_obs` database: `loco_migrator` creates the schema for the observability proxy, `loco_ingest` inserts for both collectors, and `loco_reader` serves the proxy's queries. Each user reads its password from the `password` key of a Secret named in `clickhouse.clickhouse.users`. Set `clickhouseUserPasswords.<user>` to have the chart render that Secret, or leave it empty and create the Secret yourself; the ingest Secret is also needed in the collectors' `observability-node` namespace. The proxy's connection URLs embed the `loco_reader` and `loco_migrator` passwords, so passwords may contain only letters, digits and `._~-`. Store those URLs in Secrets and name them in the `loco-control-plane` values `obsProxy.query.clickhouse.existingSecret` and `obsProxy.query.clickhouse.migratorExistingSecret`; a standalone proxy reads them from `CLICKHOUSE_URL` and `CLICKHOUSE_MIGRATOR_URL`. `env/prod/obs-chart.yaml.gotmpl` reads them from `CLICKHOUSE_MIGRATOR_PASSWORD`, `CLICKHOUSE_INGEST_PASSWORD` and `CLICKHOUSE_READER_PASSWORD`.

Set `global.region` in the `loco-obs` values to the region the cluster is registered under in the API. The collectors stamp it on every log, trace and metric as `loco.region`, and the dashboard's per-region metrics filter on it; the chart fails to render without it. Production Helmfile reads it from `LOCO_REGION`.

The chart runs ClickHouse in the cluster by default. To use an external server such as ClickHouse Cloud, set `clickhouse.enabled: false` and set `global.clickhouse.host`, `global.clickhouse.port` and `global.clickhouse.secure` to its address; ClickHouse Cloud serves the native protocol over TLS on port `9440`, so use `secure: true` there. Both collectors and the observability proxy connect to that address, and the proxy's migrations create the same `loco_obs` schema on it. Create the three users on the external server with the grants listed in `clickhouse.clickhouse.users`; the chart still renders their password Secrets from `clickhouseUserPasswords`. `clickhouse.clickhouse.extraConfig`, `keeperConfig`, `settings` and `resources` apply only to the in-cluster server.

The proxy serializes its migrations through a KeeperMap table, so the ClickHouse server needs ClickHouse Keeper and a `keeper_map_path_prefix`. The in-cluster server runs a single-node Keeper from `clickhouse.clickhouse.keeperConfig`. An external server must provide both, and `loco_migrator` must be allowed to create KeeperMap tables.

Your team supplies DNS records, certificate issuance, persistent storage, backups, and capacity. Keep credentials separate across installations and environments. Use [architecture](../reference/architecture.md) to identify the control-plane and regional boundaries.

## Upgrades

Every commit to `main` publishes the agent, builder, controller, API, observability proxy and UI images tagged `sha-<commit>`, and each binary reports that tag as its version. The charts have no default tag for these images: set `agent.image.tag`, `controller.image.tag`, `builds.builderImage.tag` and, in `loco-control-plane`, `obsProxy.image.tag` to the same `sha-<commit>`. Regenerate CRDs through `mise run controller:gen` when developing schema changes. Review database migrations and infrastructure changes before upgrading a running installation.

A supported production packaging and upgrade procedure is not established by these docs. Inspect the chart values and the release's configuration before deployment.
