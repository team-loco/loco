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
| Regional observability proxy | `observability-proxy/` | Access to regional observability |

## Installation inputs

Configure the API's database, cache, authentication, registry, and API/dashboard endpoints using the repository's [environment template](https://github.com/team-loco/loco/blob/main/.env.example). Check the template and deployed release together; authentication and registry changes are under active review. Choose a [secrets key provider](secrets-key-provider.md) for the key that protects environment secrets.

Set `DATABASE_LISTEN_URL` to a direct PostgreSQL or session-pooler URL when `DATABASE_URL` uses a transaction-mode pooler. The API holds this connection for placement notifications and uses `DATABASE_URL` for ordinary queries. Both URLs must reach the same database. With `DATABASE_LISTEN_URL` empty, the listener uses `DATABASE_URL`.

`LOCO_PLACEMENT_RETRY_DELAY` sets how long the API defers a placement after the agent reports an apply failure it will not retry. The default is `1h`. Inventory reports and new connections respect the delay; after it expires, the next inventory or new connection can resend the placement. A new desired revision clears the delay immediately.

The cluster needs Cilium, Envoy Gateway, cert-manager, and the Loco controller. Observability uses OpenTelemetry, ClickHouse, and the observability proxy. The charts and environment values define their configuration.

Set `obsProxy.controlPlane.url` to the API URL reachable from the observability namespace. Production Helmfile reads the agent and proxy's API URL from `CONTROL_PLANE_URL`; local Helmfile derives the host API port from `APP_PORT`.

Create a Secret in the observability proxy's namespace with a `token` key containing an API token for readiness checks, then set `obsProxy.auth.existingSecret` to its name. Helmfile reads this name from `OBS_PROXY_AUTH_SECRET`; production requires it and local development defaults to `loco-obs-obs-proxy-auth`. Kubernetes requires the Secret and its `token` key before starting the proxy. A standalone proxy requires `PROXY_AUTH_TOKEN` at startup. Readiness requires completed ClickHouse migrations, a successful database ping, and an authenticated response from the API permission endpoint. Each API readiness call bypasses the permission cache and uses `PERMISSION_CHECK_TIMEOUT`, supplied by `obsProxy.guardrails.permissionCheckTimeoutSeconds` (5 seconds by default). Standalone startup requires a positive duration for this setting.

The proxy authenticates each query's permission check with the query's bearer token. Signed OIDC sessions and Loco API or session tokens use the API's authentication path and require read access to the requested workspace. Permission results use the proxy's configured cache lifetime; readiness always uses the separate `PROXY_AUTH_TOKEN` credential.

Set `obsProxy.cors.allowedOrigins` to the dashboard origins that may call the regional proxy, such as `https://loco.build`. Both Helmfile environments read the comma-separated list from `CORS_ALLOWED_ORIGINS`; a standalone proxy reads the same variable. Origins must use HTTP or HTTPS and contain no credentials, path, query or fragment. An empty list disables cross-origin browser access. The proxy answers Connect preflights and exposes protocol response headers for configured origins; requests still require a bearer token and workspace permissions.

The `loco-obs` chart defines three ClickHouse users, each limited to the `loco_obs` database: `loco_migrator` creates the schema for the observability proxy, `loco_ingest` inserts for both collectors, and `loco_reader` serves the proxy's queries. Each user reads its password from the `password` key of a Secret named in `clickhouse.clickhouse.users`. Set `clickhouseUserPasswords.<user>` to have the chart render that Secret, or leave it empty and create the Secret yourself; the ingest Secret is also needed in the collectors' `observability-node` namespace. The proxy puts these passwords in its connection URLs, so they may contain only letters, digits and `._~-`. `env/prod/obs-chart.yaml.gotmpl` reads them from `CLICKHOUSE_MIGRATOR_PASSWORD`, `CLICKHOUSE_INGEST_PASSWORD` and `CLICKHOUSE_READER_PASSWORD`.

The chart runs ClickHouse in the cluster by default. To use an external server such as ClickHouse Cloud, set `clickhouse.enabled: false` and set `global.clickhouse.host`, `global.clickhouse.port` and `global.clickhouse.secure` to its address; ClickHouse Cloud serves the native protocol over TLS on port `9440`, so use `secure: true` there. Both collectors and the observability proxy connect to that address, and the proxy's migrations create the same `loco_obs` schema on it. Create the three users on the external server with the grants listed in `clickhouse.clickhouse.users`; the chart still renders their password Secrets from `clickhouseUserPasswords`. `clickhouse.clickhouse.extraConfig`, `settings` and `resources` apply only to the in-cluster server.

Your team supplies DNS records, certificate issuance, persistent storage, backups, and capacity. Keep credentials separate across installations and environments. Use [architecture](../reference/architecture.md) to identify the control-plane and regional boundaries.

## Upgrades

Every commit to `main` publishes the agent, builder, controller, API, observability proxy and UI images tagged `sha-<commit>`, and each binary reports that tag as its version. The charts have no default tag for these images: set `agent.image.tag`, `controller.image.tag`, `builds.builderImage.tag` and `obsProxy.image.tag` to the same `sha-<commit>`. Regenerate CRDs through `mise run controller:gen` when developing schema changes. Review database migrations and infrastructure changes before upgrading a running installation.

A supported production packaging and upgrade procedure is not established by these docs. Inspect the chart values and the release's configuration before deployment.
