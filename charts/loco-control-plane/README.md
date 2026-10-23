# loco-control-plane

`loco-control-plane` installs one region's dashboard, API and private observability proxy. The API serves ordinary RPCs and forwards observability RPCs to the proxy at the same public API address. Agents connect to this API with the shared connection secret; user authentication uses the configured identity provider.

Provide these dependencies before installing the chart:

| Dependency | Configuration |
| --- | --- |
| PostgreSQL | `DATABASE_URL` in the API environment Secret; use a direct connection for startup migrations. Set `DATABASE_LISTEN_URL` to a direct connection if normal API queries use a transaction pooler. |
| Identity provider | `AUTH_ISSUERS` in the API environment Secret, with the issuer, audience and web-client settings supported by the API. |
| Source object storage | Existing S3-compatible bucket and `LOCO_SOURCE_BUCKET*` API environment settings. Without a bucket, source builds are disabled. |
| Application image registry | Existing registry and `LOCO_REGISTRY_*` API environment settings. Builders and worker nodes must also reach this registry. |
| Regional ClickHouse | Install `loco-obs` or provide an external ClickHouse with Keeper. Supply separate reader and migrator DSNs through existing Secrets. |
| Envoy Gateway | Existing `eg` Gateway in `gatewayNamespace`, with a TLS listener covering `controlPlaneHostname` and `ui.hostname` and permission to attach routes from the control-plane namespace. |

Create the API environment Secret outside Helm. The chart reads environment-variable keys directly from that Secret, so credentials do not enter Helm values or the release manifest. A complete installation configures `DATABASE_URL`, `AUTH_ISSUERS`, the source bucket and registry, and a supported `LOCO_SECRETS_KEY_PROVIDER`. Store local key material or transit credentials in the same operator-managed Secret. Existing API configuration names and rotation procedures apply.

```sh
kubectl -n loco-system create secret generic regional-api-env \
  --from-env-file=/secure/loco-api.env
kubectl -n loco-system create secret generic regional-clickhouse-reader \
  --from-file=url=/secure/clickhouse-reader-url
kubectl -n loco-system create secret generic regional-clickhouse-migrator \
  --from-file=url=/secure/clickhouse-migrator-url
```

Set nonsecret API options through `api.env`. The chart supplies `APP_PORT` and `OBSERVABILITY_PROXY_URL` to connect its own Services.

```yaml
controlPlaneHostname: api.region.example.com
ui:
  hostname: app.region.example.com
  image:
    tag: sha-COMMIT@sha256:DIGEST
gatewayNamespace: loco-system
api:
  image:
    tag: sha-COMMIT@sha256:DIGEST
  existingSecret: regional-api-env
  env:
    APP_ENV: PRODUCTION
    WEB_URL: https://app.region.example.com
    CORS_ALLOWED_ORIGINS: https://app.region.example.com
obsProxy:
  image:
    tag: sha-COMMIT@sha256:DIGEST
  query:
    clickhouse:
      existingSecret:
        name: regional-clickhouse-reader
        key: url
      migratorExistingSecret:
        name: regional-clickhouse-migrator
        key: url
```

Install from the checked-out release or its published chart artifact with these values:

```sh
helm upgrade --install regional ./charts/loco-control-plane \
  --namespace loco-system --create-namespace \
  --values /secure/regional-values.yaml --wait --timeout 10m
```

For `loco-obs` in another namespace, set its `controlPlaneNamespace` to the namespace containing this chart. That allows only observability-proxy pods from the control-plane namespace to reach ClickHouse's native port. The proxy reader and migrator DSNs use the regional ClickHouse endpoint; no cross-region replication is configured. Worker-side collectors send telemetry to that region's ingestion endpoint. Query clients select one regional API address.

The API runs database migrations during normal startup while holding the existing PostgreSQL migration lock. There is no migration Job or separate migration command. Its startup probe allows five minutes before liveness checks begin; adjust `api.startupProbe` for larger databases. Existing Secrets remain owned by the operator. After rotating their values, restart the corresponding Deployment so it reads the new environment.

The dashboard serves its API at `/api` on the dashboard's own origin. Its HTTPRoute removes that prefix before forwarding to the API, including observability RPCs. A mounted public runtime configuration overrides the released UI image's compiled API address, so the same published image works at arbitrary installation hostnames. The separate `controlPlaneHostname` continues to serve CLI and agent traffic at its root. When both hostnames are equal, use `https://HOST/api` as the CLI/agent API base address; the chart suppresses the competing root API route. Set `ui.enabled=false` for a headless installation. Set `WEB_URL` to the dashboard URL in the API configuration, and register that URL's `/auth/callback` with the identity provider.

API routes retry once when a connection fails before the upstream handles the request (`connect-failure`, `reset-before-request`, or `refused-stream`). This allows an available alternate API replica to handle the request during endpoint withdrawal. Handled HTTP errors are not retried. The policy targets only named API rules, including the dashboard’s `/api` rule; dashboard routes and streaming timeouts remain unchanged. The retry is bounded and can still fail, including when a healthy alternate replica exists.

The proxy image runs as two Deployments that share `obsProxy.image` and `obsProxy.imagePullPolicy`: `<release>-obs-proxy-query`, configured under `obsProxy.query`, and `<release>-obs-proxy-ingest`, configured under `obsProxy.ingest`. `obsProxy.enabled: false` renders neither and leaves `OBSERVABILITY_PROXY_URL` unset, so the API answers observability requests with `Unavailable`. `obsProxy.ingest.enabled: true` adds the OTLP ingest Deployment and Service, with the `loco_ingest` DSN in `obsProxy.ingest.clickhouse.existingSecret`; only pods in the release namespace can reach it. The query role exposes only a ClusterIP Service. Its ingress policy permits API pods, and its permission checks use the chart's private API Service. No public observability route or cluster endpoint discovery is required.

Validate rendering and a Kubernetes installation with:

```sh
mise run test:chart-contracts
mise run test:platform-render
mise run e2e:control-plane
```

The E2E suite builds the actual dashboard, API and proxy images, installs PostgreSQL and ClickHouse/Keeper fixtures in a fresh kind cluster, and installs this chart with two API replicas against an empty database. It checks startup migrations, the dashboard and runtime configuration, Config RPCs through the dashboard origin, and an observability request forwarded through the API whose permission check returns through the private API Service. It also renders all three API/dashboard hostname layouts against Envoy, measures the reduction in failed connections to a mixed healthy/closed endpoint set, recording any exhausted retries, checks that handled HTTP 500/503 responses execute once, and compares intact 8 KiB and 2 MiB request bodies. The 2 MiB control uses only a healthy endpoint and does not assert retry buffering for large requests. Fixtures use ephemeral storage and are not production database templates. `--no-teardown` keeps the cluster; `--skip-build` reuses the images on a subsequent run.
