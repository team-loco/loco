# loco-control-plane

`loco-control-plane` installs one region's dashboard, API, observability proxy, Gateway, OpenBao and, optionally, ClickHouse. The API serves ordinary RPCs and the observability RPCs at the same public API address, reading ClickHouse directly with a read-only user; the observability proxy only ingests OTLP. Agents enroll with a single-use join token and connect with their persisted identity; user authentication uses the configured identity provider.

Provide these dependencies before installing the chart:

| Dependency | Configuration |
| --- | --- |
| PostgreSQL | `DATABASE_URL` in the API environment Secret; use a direct connection for startup migrations. Set `DATABASE_LISTEN_URL` to a direct connection if normal API queries use a transaction pooler. |
| Identity provider | `AUTH_ISSUERS` in the API environment Secret, with the issuer, audience and web-client settings supported by the API. |
| Source object storage | Existing S3-compatible bucket and `LOCO_SOURCE_BUCKET*` API environment settings. Without a bucket, source builds are disabled. |
| Application image registry | Existing registry and `LOCO_REGISTRY_*` API environment settings. Builders and worker nodes must also reach this registry. |
| Regional ClickHouse | `clickhouse.enabled: true` runs it in the release namespace, or provide an external ClickHouse with Keeper and supply the API reader, proxy migrator and proxy ingest DSNs through existing Secrets. |
| cert-manager | The cert-manager controller, which also issues OpenBao's serving certificate. The chart issues one Certificate for the Gateway hostnames with the Issuer set in `certManager.issuer`, the same options as `loco-worker`: `type: acme` solves DNS-01 through Cloudflare with `secrets.cloudflareToken` unless `certManager.issuer.solvers` supplies cert-manager's own solver list, `selfSigned` needs no token. `certManager.issuer.create: false` uses an existing issuer by `name` and `kind`. `gateway.tlsSecretName` serves your own Secret without a Gateway Certificate; `certManager.enabled: false` serves plain HTTP on port 80. A self-signed ClusterIssuer with `certManager.issuer.create: true` is created independently of the Gateway certificate, so workers on the same cluster can reference it. |
| Envoy Gateway | The Envoy Gateway controller in `gatewayNamespace`. The chart creates its own GatewayClass, EnvoyProxy and Gateway; `gateway.create: false` attaches the routes to the existing Gateway `gateway.name` instead. |

Create the API environment Secret outside Helm. The chart reads environment-variable keys directly from that Secret, so credentials do not enter Helm values or the release manifest. A complete installation configures `DATABASE_URL`, `AUTH_ISSUERS`, the source bucket and the registry. Existing API configuration names and rotation procedures apply.

```sh
kubectl -n loco-system create secret generic regional-api-env \
  --from-env-file=/secure/loco-api.env
```

`api.systemAdmins` lists the emails the API makes system admins on their first verified sign-in, as `LOCO_SYSTEM_ADMINS`; see [self-hosting](../../docs/content/operations/self-hosting.md#first-system-admin).

`api.invitationTtl` sets `INVITATION_TTL`. Core sends no email; the inviter copies and shares the invitation link. Installations that want email consume `invitation.created` and `invitation.resent` through [`INSTALL_WEBHOOKS`](../../docs/content/operations/webhooks.md).

Typed settings under `api.limits` expose every install count limit, including resource and membership caps, history counts and telemetry retention days. `api.defaults` exposes service CPU, memory, replicas, autoscaling, health and deployment defaults. `api.retention.events` sets `EVENTS_RETENTION`; `api.retention.images` sets `LOCO_IMAGE_RETENTION`. `api.joinToken.ttl` and `api.joinToken.maxTtl` set the default and maximum join-token lifetimes. Null numeric settings and empty strings preserve the API's defaults. Both chart schemas validate value types and bounds. A typed setting and its corresponding `api.env` entry cannot both be supplied.

```yaml
api:
  limits:
    resourcesPerWorkspace: 200
    logRetentionDays: 30
  defaults:
    cpu: 200m
    minReplicas: 2
    maxReplicas: 4
  retention:
    events: 2160h
  joinToken:
    maxTtl: 168h
```

Set other nonsecret API options through `api.env`. The chart supplies `APP_PORT` and, while `obsProxy.enabled` is true, `CLICKHOUSE_URL` and `CLICKHOUSE_DB`; `api.env` cannot override them.

Open log tails, deployment watches and event streams periodically recheck access. Set these optional API settings through `api.env` or the API environment Secret:

| Variable | Default | Meaning |
| --- | --- | --- |
| `LOCO_STREAM_AUTH_INTERVAL` | `5s` | Positive duration between credential and permission checks, including idle streams. |
| `LOCO_STREAM_AUTH_TIMEOUT` | `2s` | Positive timeout for each check and the write grace before aborting a blocked HTTP stream. |

See [open streams and access changes](../../docs/content/operations/access.md#open-streams-and-access-changes) for revocation timing.

`LOCO_TARGET_PLATFORM` selects the installation's application processor: `linux/amd64` (default) or `linux/arm64`. Set it through `api.env` or the API environment Secret. The API rejects other values at startup. Workers report the platforms their actual Kubernetes nodes support. New deployments and builds require a compatible worker. Builds run on native matching nodes, pass the explicit target to BuildKit and validate the resulting image configuration before publishing. Application Pods select only nodes matching the target. The API machine's processor does not choose this value.

The target is saved with every build and deployment. Changing the installation target requires rebuilding source images for new deployments; it does not rewrite the target of a retained deployment or rollback. A multi-architecture worker can run both old and new snapshots on their matching nodes.

`LOCO_PUBLIC_IMAGE_MAX_BYTES` sets the API's prebuilt public-image budget in bytes, defaulting to `1073741824` (1 GiB). Set it through `api.env` or the API environment Secret; it must be a positive integer. The API sums manifest-declared sizes of unique compressed layer blobs and the image config for each variant eligible for the installation target, counting a repeated digest once within each variant. Each variant must fit independently because workers choose the variant from the pinned index, including CPU-specific variants, entries with an omitted platform and nested indexes. Other platforms and manifest JSON are excluded. Invalid or conflicting blob sizes are rejected. An over-budget image fails during resolution, before deployment or placement writes, with the measured size and limit in bytes. Images remain pinned to the immutable digest of the original image or index, and the selected variant must support the installation target.

Source-built images use the worker chart's `builds.maxImageSize` budget (default `1Gi`), enforced by the builder before pushing. That budget counts the actual unique blobs in the OCI output layout, including manifest blobs. Neither budget limits the decompressed filesystem size. Configure the two budgets separately for the installation's pull and storage capacity.

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
obsProxy:
  image:
    tag: sha-COMMIT@sha256:DIGEST
openbao:
  seal:
    static:
      existingSecret:
        name: openbao-seal
  snapshots:
    s3:
      endpoint: https://s3.region.example.com
      bucket: region-openbao-snapshots
      existingSecret:
        name: openbao-snapshots-s3
clickhouse:
  enabled: true
```

The chart runs OpenBao for the API's secrets key and the `ed25519` keys `openbao.transit.ingestKey` and `openbao.transit.registryKey` that sign ingest and registry access tokens, and sets the API's `LOCO_SECRETS_TRANSIT_*` and `LOCO_INGEST_TRANSIT_KEY` settings. `api.registry.auth: token` also sets `LOCO_REGISTRY_AUTH=token`, `LOCO_REGISTRY_TRANSIT_KEY` and `LOCO_REGISTRY_TRANSIT_KEY_VERSION` from `api.registry.transitKeyVersion` (1 by default) and `LOCO_REGISTRY_TOKEN_SERVICE` from `api.registry.service`, which token mode requires; the default `static` sets none of them; the API logs in to OpenBao with its ServiceAccount `<release>-api`. Create its seal key Secret, named in `openbao.seal.static.existingSecret.name`, before installing; OpenBao serves TLS from a CA the chart issues with cert-manager, so the cert-manager controller is required even with `certManager.enabled: false`. [The secrets key provider](../../docs/content/operations/secrets-key-provider.md#openbao-in-the-control-plane-chart) describes the seal options, the setup Job, the recovery keys it writes to `<release>-openbao-init` for you to move offline, and an external OpenBao.

```sh
openssl rand -base64 32 | tr -d '\n' |
  kubectl -n loco-system create secret generic openbao-seal --from-file=key=/dev/stdin
```

The chart also uploads hourly raft snapshots to the region's object storage; set `openbao.snapshots.s3.endpoint`, `openbao.snapshots.s3.bucket` and the Secret `openbao.snapshots.s3.existingSecret` with the `access-key-id` and `secret-access-key` keys, or `openbao.snapshots.enabled: false`.

For in-cluster ClickHouse, Helm renders Secrets named by `clickhouse.clickhouse.users[].password_secret_name`. Each holds `password` and `url` keys; the URL is `clickhouse://USER:PASSWORD@clickhouse-CHI_NAME.NAMESPACE.svc.cluster.local:9000`, where `CHI_NAME` is `clickhouse.fullnameOverride`. The operator Secret, named by `clickhouse.operator.fullnameOverride`, holds `username` and `password`. Helm reads existing Secret data with `lookup` and generates a 64-character hex password only when the Secret is missing. The Secrets carry `helm.sh/resource-policy: keep` and survive uninstall. Credential data is present in the Helm release; restrict access to release Secrets. Flux takes ownership of existing resources by default. For direct Helm adoption of operator-created Secrets, pass `--take-ownership`.

`setup.ttlSecondsAfterFinished` cleans the completed OpenBao setup Job after 3600 seconds by default. `openbao.setup.timeoutSeconds` bounds setup. Flux drift detection ignores the Job so TTL cleanup does not cause another run.

With an external ClickHouse, leave `clickhouse.enabled: false` and name Secrets holding each user's URL in `api.clickhouse.existingSecret` (read-only), `obsProxy.ingest.clickhouse.migratorExistingSecret` and `obsProxy.ingest.clickhouse.existingSecret`.

Install from the checked-out release or its published chart artifact with these values:

```sh
helm upgrade --install regional ./charts/loco-control-plane \
  --namespace loco-system --create-namespace \
  --values /secure/regional-values.yaml --wait --wait-for-jobs --timeout 10m
```

The Gateway has one listener per hostname: `ui.hostname`, `controlPlaneHostname` and, when ingest is published, `obsProxy.ingest.hostname`. HTTPS on 443 with the Secret from cert-manager or `gateway.tlsSecretName`, HTTP on port 80 when `certManager.enabled` is false. `gateway.service` takes the same Envoy Service settings as `loco-worker`'s `envoyProxy.service`. Worker collectors export to the ingest hostname; the API reader and proxy migrator DSNs use the regional ClickHouse, and no cross-region replication is configured.

The API runs database migrations during normal startup while holding the existing PostgreSQL migration lock. There is no migration Job or separate migration command. Its startup probe allows five minutes before liveness checks begin; adjust `api.startupProbe` for larger databases. Existing Secrets remain owned by the operator. After rotating their values, restart the corresponding Deployment so it reads the new environment.

The dashboard serves its API at `/api` on the dashboard's own origin. Its HTTPRoute removes that prefix before forwarding to the API, including observability RPCs. A mounted public runtime configuration overrides the released UI image's compiled API address, so the same published image works at arbitrary installation hostnames. The separate `controlPlaneHostname` continues to serve CLI and agent traffic at its root. When both hostnames are equal, use `https://HOST/api` as the CLI/agent API base address; the chart suppresses the competing root API route. Set `ui.enabled=false` for a headless installation. When `AUTH_ISSUERS` configures a dashboard client, set `api.env.WEB_URL` to the dashboard origin and register that origin's `/auth/callback` with the identity provider. Set `web.clientId` and, for a confidential client, `web.clientSecret` in the API environment Secret. When running multiple API replicas, set `CACHE_TYPE=valkey` and point `CACHE_ADDR` at the same Valkey instance for every replica. A sign-in can start and finish on different replicas.

API routes retry once when a connection fails before the upstream handles the request (`connect-failure`, `reset-before-request`, or `refused-stream`). This allows an available alternate API replica to handle the request during endpoint withdrawal. Handled HTTP errors are not retried. The policy targets only named API rules, including the dashboard’s `/api` rule; dashboard routes and streaming timeouts remain unchanged. The retry is bounded and can still fail, including when a healthy alternate replica exists.

The observability proxy runs as the Deployment `<release>-obs-proxy-ingest`, using `obsProxy.image` and `obsProxy.imagePullPolicy` and configured under `obsProxy.ingest`, with an OTLP Service reachable from pods in the release namespace. Before it ingests, it runs the ClickHouse schema migrations as the migrator user, and it checks ingest tokens through the chart's private API Service. Setting `obsProxy.ingest.hostname` publishes `/v1/` on that hostname through the Gateway. The hostname has no default. `obsProxy.enabled: false` renders no proxy and leaves the API's `CLICKHOUSE_URL` unset, so the API answers observability requests with `Unavailable`. ClickHouse's network policy admits the proxy and the API pods on port 9000.

Validate rendering and a Kubernetes installation with:

```sh
mise run test:charts
mise run test:platform-render
mise run e2e controlplane TestControlPlane
```

The E2E suite builds the dashboard, API and proxy images, installs PostgreSQL and the Envoy Gateway controller in a fresh kind cluster, and installs this chart with its own Gateway, in-cluster ClickHouse, a one-node OpenBao, published ingest and two API replicas against an empty database. It checks that an API pod that restarted before the setup Job created the Transit keys logged why, the Gateway's listeners, startup migrations, the dashboard and runtime configuration, Config RPCs through the dashboard origin, an observability request the API refuses for an invalid token, an OTLP/HTTP log sent to the ingest hostname that lands in ClickHouse, an ingest token the API signs with the chart OpenBao's Transit key, secrets the API encrypts with the chart's OpenBao before and after an upgrade reruns its setup Job, and a raft snapshot from the snapshot CronJob restored into a reinstalled OpenBao. It then renders all three API/dashboard hostname layouts against the Gateway, measures the reduction in failed connections to a mixed healthy/closed endpoint set, checks that handled HTTP 500/503 responses execute once, and compares intact 8 KiB and 2 MiB request bodies. `--keep` keeps the cluster; `--skip-build` reuses the images on a subsequent run.
