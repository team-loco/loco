# loco-control-plane

`loco-control-plane` installs one region's dashboard, API, observability proxy, Gateway, OpenBao and, optionally, ClickHouse. The API serves ordinary RPCs and forwards observability RPCs to the proxy at the same public API address. Agents connect to this API with the shared connection secret; user authentication uses the configured identity provider.

Provide these dependencies before installing the chart:

| Dependency | Configuration |
| --- | --- |
| PostgreSQL | `DATABASE_URL` in the API environment Secret; use a direct connection for startup migrations. Set `DATABASE_LISTEN_URL` to a direct connection if normal API queries use a transaction pooler. |
| Identity provider | `AUTH_ISSUERS` in the API environment Secret, with the issuer, audience and web-client settings supported by the API. |
| Source object storage | Existing S3-compatible bucket and `LOCO_SOURCE_BUCKET*` API environment settings. Without a bucket, source builds are disabled. |
| Application image registry | Existing registry and `LOCO_REGISTRY_*` API environment settings. Builders and worker nodes must also reach this registry. |
| Regional ClickHouse | `clickhouse.enabled: true` runs it in the release namespace, or provide an external ClickHouse with Keeper and supply reader, migrator and ingest DSNs through existing Secrets. |
| cert-manager | The cert-manager controller, which also issues OpenBao's serving certificate. The chart issues one Certificate for the Gateway hostnames with the Issuer set in `certManager.issuer`, the same options as `loco-worker`: `type: acme` solves DNS-01 through Cloudflare with `secrets.cloudflareToken` unless `certManager.issuer.solvers` supplies cert-manager's own solver list, `selfSigned` needs no token. `certManager.issuer.create: false` uses an existing issuer by `name` and `kind`. `gateway.tlsSecretName` serves your own Secret and renders no cert-manager objects; `certManager.enabled: false` serves plain HTTP on port 80. |
| Envoy Gateway | The Envoy Gateway controller in `gatewayNamespace`. The chart creates its own GatewayClass, EnvoyProxy and Gateway; `gateway.create: false` attaches the routes to the existing Gateway `gateway.name` instead. |

Create the API environment Secret outside Helm. The chart reads environment-variable keys directly from that Secret, so credentials do not enter Helm values or the release manifest. A complete installation configures `DATABASE_URL`, `AUTH_ISSUERS`, the source bucket and registry, and the `LOCO_SECRETS_TRANSIT_TOKEN` the API presents to OpenBao. Existing API configuration names and rotation procedures apply.

```sh
kubectl -n loco-system create secret generic regional-api-env \
  --from-env-file=/secure/loco-api.env
```

`api.bootstrapToken.existingSecret` names the Secret key holding the one-time `LOCO_BOOTSTRAP_TOKEN` for `locoadm bootstrap`. Remove it after the first system admin exists; see [self-hosting](../../docs/content/operations/self-hosting.md#first-system-admin).

Set nonsecret API options through `api.env`. The chart supplies `APP_PORT` and `OBSERVABILITY_PROXY_URL` to connect its own Services.

```yaml
controlPlaneHostname: api.region.example.com
ui:
  hostname: app.region.example.com
  image:
    tag: sha-COMMIT@sha256:DIGEST
gatewayNamespace: loco-system
certManager:
  issuer:
    email: ops@region.example.com
secrets:
  cloudflareToken: CLOUDFLARE_API_TOKEN
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
  ingest:
    enabled: true
openbao:
  seal:
    static:
      existingSecret:
        name: openbao-seal
clickhouse:
  enabled: true
clickhouseUserPasswords:
  loco_migrator: MIGRATOR_PASSWORD
  loco_ingest: INGEST_PASSWORD
  loco_reader: READER_PASSWORD
```

The chart runs OpenBao for the API's secrets key and sets the API's `LOCO_SECRETS_TRANSIT_ADDR`, `LOCO_SECRETS_TRANSIT_MOUNT` and `LOCO_SECRETS_TRANSIT_KEY`. Create its seal key Secret, named in `openbao.seal.static.existingSecret.name`, before installing; OpenBao serves TLS from a CA the chart issues with cert-manager, so the cert-manager controller is required even with `certManager.enabled: false`. [The secrets key provider](../../docs/content/operations/secrets-key-provider.md#openbao-in-the-control-plane-chart) describes the seal options, the setup Job, the recovery keys it writes to `<release>-openbao-init` for you to move offline, and an external OpenBao.

```sh
kubectl -n loco-system create secret generic openbao-seal --from-literal=key="$(openssl rand -base64 32)"
```

With an external ClickHouse, leave `clickhouse.enabled: false` and name Secrets holding each user's URL in `obsProxy.query.clickhouse.existingSecret`, `obsProxy.query.clickhouse.migratorExistingSecret` and `obsProxy.ingest.clickhouse.existingSecret`.

Install from the checked-out release or its published chart artifact with these values:

```sh
helm upgrade --install regional ./charts/loco-control-plane \
  --namespace loco-system --create-namespace \
  --values /secure/regional-values.yaml --wait --timeout 10m
```

The Gateway has one listener per hostname: `ui.hostname`, `controlPlaneHostname` and, when ingest is published, `obsProxy.ingest.hostname`. HTTPS on 443 with the Secret from cert-manager or `gateway.tlsSecretName`, HTTP on port 80 when `certManager.enabled` is false. `gateway.service` takes the same Envoy Service settings as `loco-worker`'s `envoyProxy.service`. Worker collectors export to the ingest hostname; the proxy reader and migrator DSNs use the regional ClickHouse, and no cross-region replication is configured.

The API runs database migrations during normal startup while holding the existing PostgreSQL migration lock. There is no migration Job or separate migration command. Its startup probe allows five minutes before liveness checks begin; adjust `api.startupProbe` for larger databases. Existing Secrets remain owned by the operator. After rotating their values, restart the corresponding Deployment so it reads the new environment.

The dashboard serves its API at `/api` on the dashboard's own origin. Its HTTPRoute removes that prefix before forwarding to the API, including observability RPCs. A mounted public runtime configuration overrides the released UI image's compiled API address, so the same published image works at arbitrary installation hostnames. The separate `controlPlaneHostname` continues to serve CLI and agent traffic at its root. When both hostnames are equal, use `https://HOST/api` as the CLI/agent API base address; the chart suppresses the competing root API route. Set `ui.enabled=false` for a headless installation. Set `WEB_URL` to the dashboard URL in the API configuration, and register that URL's `/auth/callback` with the identity provider.

API routes retry once when a connection fails before the upstream handles the request (`connect-failure`, `reset-before-request`, or `refused-stream`). This allows an available alternate API replica to handle the request during endpoint withdrawal. Handled HTTP errors are not retried. The policy targets only named API rules, including the dashboard’s `/api` rule; dashboard routes and streaming timeouts remain unchanged. The retry is bounded and can still fail, including when a healthy alternate replica exists.

The proxy image runs as two Deployments that share `obsProxy.image` and `obsProxy.imagePullPolicy`: `<release>-obs-proxy-query`, configured under `obsProxy.query`, and `<release>-obs-proxy-ingest`, configured under `obsProxy.ingest`. `obsProxy.enabled: false` renders neither and leaves `OBSERVABILITY_PROXY_URL` unset, so the API answers observability requests with `Unavailable`. `obsProxy.ingest.enabled: true` adds the OTLP ingest Deployment and Service, reachable from pods in the release namespace. Setting `obsProxy.ingest.hostname` as well publishes `/v1/` on that hostname through the Gateway. Ingest does not authenticate requests yet, so the hostname is empty by default. The query role exposes only a ClusterIP Service. Its ingress policy permits API pods, and its permission checks use the chart's private API Service. The query role has no public route.

Validate rendering and a Kubernetes installation with:

```sh
mise run test:charts
mise run test:platform-render
mise run e2e:control-plane
```

The E2E suite builds the dashboard, API and proxy images, installs PostgreSQL and the Envoy Gateway controller in a fresh kind cluster, and installs this chart with its own Gateway, in-cluster ClickHouse, a one-node OpenBao, published ingest and two API replicas against an empty database. It checks the Gateway's listeners, startup migrations, the dashboard and runtime configuration, Config RPCs through the dashboard origin, an observability request forwarded through the API whose permission check returns through the private API Service, an OTLP/HTTP log sent to the ingest hostname that lands in ClickHouse, and secrets the API encrypts with the chart's OpenBao before and after an upgrade reruns its setup Job. It then renders all three API/dashboard hostname layouts against the Gateway, measures the reduction in failed connections to a mixed healthy/closed endpoint set, checks that handled HTTP 500/503 responses execute once, and compares intact 8 KiB and 2 MiB request bodies. `--no-teardown` keeps the cluster; `--skip-build` reuses the images on a subsequent run.
