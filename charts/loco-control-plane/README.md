# loco-control-plane

`loco-control-plane` installs one region's dashboard, API, observability proxy, Gateway, OpenBao and, optionally, ClickHouse. The API serves ordinary RPCs and the observability RPCs at the same public API address, reading ClickHouse directly with a read-only user; the observability proxy only ingests OTLP. Agents enroll with a single-use join token and connect with their persisted identity; user authentication uses the configured identity provider.

Provide these dependencies before installing the chart:

| Dependency | Configuration |
| --- | --- |
| PostgreSQL | `DATABASE_URL` in the API environment Secret; use a direct connection for startup migrations. Set `DATABASE_LISTEN_URL` to a direct connection if normal API queries use a transaction pooler. Or `postgres.enabled: true` runs it on CloudNativePG; see [Bundled PostgreSQL](#bundled-postgresql). |
| Identity provider | `AUTH_ISSUERS` in the API environment Secret, with the issuer, audience and web-client settings supported by the API. |
| Source object storage | Existing S3-compatible bucket and `LOCO_SOURCE_BUCKET*` API environment settings. Without a bucket, source builds are disabled. |
| Application image registry | Existing registry and `LOCO_REGISTRY_*` API environment settings. Builders and worker nodes must also reach this registry. Or `registry.enabled: true` runs one; see [Bundled registry](#bundled-registry). |
| Regional ClickHouse | `clickhouse.enabled: true` runs it in the release namespace, or provide an external ClickHouse with Keeper and supply the API reader, proxy migrator and proxy ingest DSNs through existing Secrets. |
| cert-manager | The cert-manager controller, which also issues OpenBao's serving certificate. The chart issues one Certificate for the Gateway hostnames with the Issuer set in `certManager.issuer`, the same options as `loco-worker`: `type: acme` needs `certManager.issuer.email` and `certManager.issuer.solvers`, cert-manager's own solver list, which the chart renders verbatim; create any Secret a solver references yourself. `selfSigned` needs neither. `certManager.issuer.create: false` uses an existing issuer by `name` and `kind`. `gateway.tlsSecretName` serves your own Secret without a Gateway Certificate; `certManager.enabled: false` serves plain HTTP on port 80. A self-signed ClusterIssuer with `certManager.issuer.create: true` is created independently of the Gateway certificate, so workers on the same cluster can reference it. |
| Envoy Gateway | The Envoy Gateway controller in `gatewayNamespace`. The chart creates its own GatewayClass, EnvoyProxy and Gateway; `gateway.create: false` attaches the routes to the existing Gateway `gateway.name` instead. |

Create the API environment Secret outside Helm. The chart reads environment-variable keys directly from that Secret, so credentials do not enter Helm values or the release manifest. A complete installation configures `DATABASE_URL`, `AUTH_ISSUERS`, the source bucket and the registry.

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

Set other nonsecret API options through `api.env`; [API environment](../../docs/content/reference/api-environment.md) lists every variable the API reads. The chart supplies `APP_PORT` and, while `obsProxy.enabled` is true, `CLICKHOUSE_URL` and `CLICKHOUSE_DB`; `api.env` cannot override them.

Open log tails, deployment watches and event streams periodically recheck access. Set these optional API settings through `api.env` or the API environment Secret:

| Variable | Default | Meaning |
| --- | --- | --- |
| `LOCO_STREAM_AUTH_INTERVAL` | `5s` | Positive duration between credential and permission checks, including idle streams. |
| `LOCO_STREAM_AUTH_TIMEOUT` | `2s` | Positive timeout for each check and the write grace before aborting a blocked HTTP stream; at most `LOCO_STREAM_AUTH_INTERVAL`. |

See [open streams and access changes](../../docs/content/operations/access.md#open-streams-and-access-changes) for revocation timing.

Each API replica holds at most `api.streams.maxPerReplica` streams open (500 by default) and `api.streams.maxPerCaller` for one caller (50); past either it refuses the stream with `ResourceExhausted` and counts it in the `loco.api.streams.refused` metric, beside `loco.api.streams.open`. The gateway's circuit breaker for the API counts per Envoy proxy pod: each Envoy proxy allows `api.replicaCount` × (`api.streams.maxPerReplica` + 1024) requests at once, so open streams through one proxy leave it 1,024 unary requests per API replica before it answers 503.

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
    solvers:
      - dns01:
          cloudflare:
            apiTokenSecretRef:
              name: cloudflare-api-token
              key: api-token
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

`openbao.setup.ttlSecondsAfterFinished` cleans the completed OpenBao setup Job after 3600 seconds by default. `openbao.setup.timeoutSeconds` bounds setup. Flux drift detection ignores the Job so TTL cleanup does not cause another run.

The API requests 400m CPU and 256Mi memory and is limited to 2 CPUs and 1Gi. The Go runtime sets `GOMAXPROCS` from the CPU limit, rounding a limit below 2 up to 2. Every Go container (the API, the ingest proxy and OpenBao) has a CPU limit of at least 2. The chart sets the API's `GOMEMLIMIT` from its container memory limit unless `api.env` sets it. OpenBao gets `GOMEMLIMIT` equal to its memory limit. ClickHouse requests 1 CPU and 4Gi and is limited to 4 CPUs and 4Gi.

With an external ClickHouse, leave `clickhouse.enabled: false` and name Secrets holding each user's URL in `api.clickhouse.existingSecret` (read-only), `obsProxy.ingest.clickhouse.migratorExistingSecret` and `obsProxy.ingest.clickhouse.existingSecret`.

Install from the checked-out release or its published chart artifact with these values:

```sh
helm upgrade --install regional ./charts/loco-control-plane \
  --namespace loco-system --create-namespace \
  --values /secure/regional-values.yaml --wait --wait-for-jobs --timeout 10m
```

The Gateway has one listener per hostname: `ui.hostname`, `controlPlaneHostname`, `obsProxy.ingest.hostname` when ingest is published, and each `hostname` in `gateway.extraListeners`. HTTPS on 443 with the Secret from cert-manager or `gateway.tlsSecretName`, HTTP on port 80 when `certManager.enabled` is false. The chart's own listeners admit HTTPRoutes from the release namespace. An extra listener admits HTTPRoutes from the namespaces in its `namespaces` list, or from the release namespace when the list is empty, and its hostname joins the Gateway Certificate, so services outside the chart get HTTPS on the control-plane Gateway:

```yaml
gateway:
  extraListeners:
    - hostname: auth.example.com
      namespaces: [sign-in]
```

Every listener shares one Certificate, so a hostname the issuer cannot prove fails that Certificate's single ACME order and blocks HTTPS for every listener, on a fresh install and at renewal. A Secret named in `gateway.tlsSecretName` must cover the extra hostnames itself. The NetworkPolicies in a namespace behind an extra listener must admit the Envoy pods in `gatewayNamespace`. Hostnames must be unique across the chart's listeners and `gateway.extraListeners`; the chart fails on a repeat.

`gateway.service` takes the same Envoy Service settings as `loco-worker`'s `envoyProxy.service`. Worker collectors export to the ingest hostname; the API reader and proxy migrator DSNs use the regional ClickHouse, and no cross-region replication is configured.

The API runs database migrations during normal startup while holding a PostgreSQL migration lock. There is no migration Job or separate migration command. Its startup probe allows five minutes before liveness checks begin; adjust `api.startupProbe` for larger databases. The operator owns the Secrets the chart reads. After rotating their values, restart the corresponding Deployment so it reads the new environment.

The dashboard serves its API at `/api` on the dashboard's own origin. Its HTTPRoute removes that prefix before forwarding to the API, including observability RPCs. A mounted public runtime configuration overrides the released UI image's compiled API address, so the same published image works at arbitrary installation hostnames. `ui.docsURL` sets the documentation address the dashboard links to, `https://docs.loco.build` by default. The separate `controlPlaneHostname` serves CLI and agent traffic at its root. When both hostnames are equal, use `https://HOST/api` as the CLI/agent API base address; the chart suppresses the competing root API route. Set `ui.enabled=false` for a headless installation. When `AUTH_ISSUERS` configures a dashboard client, set `api.env.WEB_URL` to the dashboard origin and register that origin's `/auth/callback` with the identity provider. Set `web.clientId` and, for a confidential client, `web.clientSecret` in the API environment Secret. The API keeps sign-in state in PostgreSQL, so a sign-in can start and finish on different API replicas.

API routes retry once when a connection fails before the upstream handles the request (`connect-failure`, `reset-before-request`, or `refused-stream`). This allows an available alternate API replica to handle the request during endpoint withdrawal. Handled HTTP errors are not retried. The policy targets only named API rules, including the dashboard’s `/api` rule; dashboard routes and streaming timeouts are outside it. The retry is bounded and can fail, even when a healthy alternate replica exists.

The dashboard and the API run `ui.replicaCount` and `api.replicaCount` replicas, 2 by default. The ingest proxy and the Gateway's Envoy proxies each scale on CPU between `minReplicas` and `maxReplicas` of `obsProxy.ingest.autoscaling` and `gateway.autoscaling`, 2 to 6 by default. The autoscalers read CPU use from the `metrics.k8s.io` API, which the loco-worker chart's metrics server provides; on a cluster without it they hold `minReplicas` and report `FailedGetResourceMetric` until set to `enabled: false`. With `enabled: false` they run `obsProxy.ingest.replicaCount` and `gateway.replicaCount`, 2 by default. The API, the dashboard, the ingest proxy, OpenBao and the Gateway's Envoy proxies each have a PodDisruptionBudget that lets one pod go at a time, whatever their replica count, and spread their pods across nodes by hostname with `whenUnsatisfiable: ScheduleAnyway`, so a one-node cluster still schedules them. ClickHouse has neither.

The observability proxy runs as the Deployment `<release>-obs-proxy-ingest`, using `obsProxy.image` and `obsProxy.imagePullPolicy` and configured under `obsProxy.ingest`, with an OTLP Service reachable from pods in the release namespace. Before it ingests, it runs the ClickHouse schema migrations as the migrator user, and it checks ingest tokens through the chart's private API Service. Setting `obsProxy.ingest.hostname` publishes `/v1/` on that hostname through the Gateway. The hostname has no default. `obsProxy.ingest.env` sets the proxy's optional settings, each described with its default in [the architecture reference](../../docs/content/reference/architecture.md): `MIGRATION_RETRY_BUDGET`, `MIGRATION_LOCK_TTL`, `MIGRATION_LOCK_RETRY_PERIOD`, `OTLP_MAX_REQUEST_BYTES`, `INGEST_JWKS_REFRESH_INTERVAL`, `INGEST_RETRY_AFTER`, `INGEST_PLACEMENT_CACHE_TTL`, `INGEST_RETENTION_CACHE_TTL`, `INGEST_PLACEMENT_REFETCH_INTERVAL`, `INGEST_CLOCK_SKEW` and `INGEST_MAX_RECORD_LATENESS`. The chart supplies `PORT`, `CONTROL_PLANE_URL`, `OTLP_GRPC_PORT`, `OTLP_HTTP_PORT`, `CLICKHOUSE_DB`, `CLICKHOUSE_INGEST_URL` and `CLICKHOUSE_MIGRATOR_URL`; `obsProxy.ingest.env` cannot override them. `obsProxy.enabled: false` renders no proxy and leaves the API's `CLICKHOUSE_URL` unset, so the API answers observability requests with `Unavailable`. ClickHouse's network policy admits the proxy and the API pods on port 9000.

Validate rendering and a Kubernetes installation with:

```sh
mise run test:charts
mise run test:platform-render
mise run e2e controlplane TestControlPlane
```

The E2E suite builds the dashboard, API and proxy images, installs the CloudNativePG operator and the Envoy Gateway controller in a fresh kind cluster, and installs this chart with its own Gateway, a one-instance bundled PostgreSQL, the bundled registry in token mode, in-cluster ClickHouse, a one-node OpenBao, published ingest and two API replicas against an empty database. It checks that an API pod that restarted before the setup Job created the Transit keys logged why, the Gateway's listeners, the registry's Bearer challenge naming the API's token endpoint, startup migrations, the dashboard and runtime configuration, Config RPCs through the dashboard origin, an observability request the API refuses for an invalid token, an OTLP/HTTP log sent to the ingest hostname that lands in ClickHouse, an ingest token the API signs with the chart OpenBao's Transit key, secrets the API encrypts with the chart's OpenBao before and after an upgrade reruns its setup Job, and a raft snapshot from the snapshot CronJob restored into a reinstalled OpenBao. It then renders all three API/dashboard hostname layouts against the Gateway, measures the reduction in failed connections to a mixed healthy/closed endpoint set, checks that handled HTTP 500/503 responses execute once, and compares intact 8 KiB and 2 MiB request bodies. `--keep` keeps the cluster; `--skip-build` reuses the images on a subsequent run.

## Bundled PostgreSQL

`postgres.enabled: true` runs the API database on a [CloudNativePG](https://cloudnative-pg.io) `Cluster` named `<release>-postgres` in the release namespace. It needs the CloudNativePG operator and its CRDs first; the platform bundle installs them from its `./postgres-operator` entry point, and `postgres.operatorNamespace` (`cnpg-system`) names the namespace it runs in. The cluster runs `postgres.instances` instances (2), one primary and streaming replicas, each on its own PersistentVolumeClaim of `postgres.storage.size` (10Gi) in `postgres.storage.storageClass`, the cluster default when empty. The StorageClass needs `ReadWriteOnce` volumes. `postgres.resources` sets each instance's requests and limits. The image is `postgres.image`, a CloudNativePG `minimal` image.

The bundled PostgreSQL has no backups yet: losing its volumes loses the database. Back it up yourself, for example with `pg_dump` against `<release>-postgres-rw`, until the chart supports backups to object storage.

CloudNativePG initializes the database `loco`, owned by the role `loco`, and writes its connection details to the Secret `<release>-postgres-app`. The API reads `DATABASE_URL` from that Secret's `uri` key, which points at the read-write Service `<release>-postgres-rw` on port 5432, and runs its migrations on startup as the database owner. `api.env.DATABASE_URL` is refused while `postgres.enabled` is true; a `DATABASE_URL` in the API environment Secret is overridden. The API pods wait in `CreateContainerConfigError` until the cluster's first instance has initialized and the Secret exists.

The `Cluster` carries `helm.sh/resource-policy: keep`: setting `postgres.enabled: false` or uninstalling the release leaves the cluster, its volumes and its Secrets in place. Delete the `Cluster` yourself to remove the data.

`postgres.databases` adds databases for other services on the same cluster. Each entry has a `name`, an `owner` login role, `createRole: true` to grant that role `CREATEROLE`, and `passwordSecret`, an existing `kubernetes.io/basic-auth` Secret in the release namespace whose `username` is the owner and whose `password` CloudNativePG sets on the role. The chart adds the role to the cluster's managed roles, never as a superuser, and a CloudNativePG `Database` named `<release>-postgres-<name, underscores as dashes>`. CloudNativePG keeps a database when its entry is removed. The owning service connects to `<release>-postgres-rw.<release namespace>:5432` with the role and password from its Secret:

```yaml
postgres:
  enabled: true
  databases:
    - name: cp_auth
      owner: cp_auth
      createRole: true
      passwordSecret: cp-auth-db
  clients:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: sign-in
```

The cluster's NetworkPolicy admits the API pods, the cluster's own instances and the operator namespace on port 5432, and the operator namespace on port 8000, the instance status port. `postgres.clients` adds NetworkPolicy peers admitted on port 5432, such as the namespaces of the services that own `postgres.databases`.

## Bundled registry

`registry.enabled: true` runs [zot](https://zotregistry.dev), an OCI registry, for build images as the Deployment `<release>-registry`, and serves it at `registry.hostname` through the chart's Gateway, on a listener of its own like an entry in `gateway.extraListeners`, with HTTPS from the Gateway Certificate. It needs the API's token mode, `api.registry.auth: token`; the chart refuses static mode with the bundled registry. In token mode the registry holds no passwords: it answers clients with a Bearer challenge naming the API's `/registry/token` endpoint on `controlPlaneHostname` and the service `api.registry.service`, which defaults to `registry.hostname`, and verifies the tokens the API signs with the public key the API serves at `/.well-known/loco-registry-key.pem`. An init container fetches that key from the API's private Service before the registry starts, retrying until the API serves it. The chart also sets the API's `LOCO_REGISTRY_HOST` to `registry.hostname`; `LOCO_REGISTRY_URL` keeps its default, `https://<registry.hostname>`, the address the image sweeper uses.

Images live on a PersistentVolumeClaim `<release>-registry` of `registry.storage.volume.size` (50Gi) in `registry.storage.volume.storageClass` by default, kept when the registry is disabled or the release uninstalled. `registry.storage.type: s3` stores them in an S3-compatible bucket instead:

```yaml
registry:
  enabled: true
  hostname: registry.example.com
  storage:
    type: s3
    s3:
      endpoint: https://s3.example.com
      bucket: loco-images
      existingSecret:
        name: registry-s3
api:
  registry:
    auth: token
```

`registry.storage.s3.endpoint` is empty for AWS S3, in `registry.storage.s3.region`; any other endpoint is addressed by path. Images go under `registry.storage.s3.rootDirectory` (`/registry`) in `registry.storage.s3.bucket`, and the Secret `registry.storage.s3.existingSecret.name` holds the access key ID and secret access key under `accessKeyIdKey` (`access-key-id`) and `secretAccessKeyKey` (`secret-access-key`). Either way the registry runs one replica: zot keeps its metadata on local disk. Every six hours it deletes blobs no image has referenced for an hour.

Worker clusters use the same settings as for any token-mode registry: the `loco-worker` values `registry.auth: token` and `env.LOCO_REGISTRY_HOST: <registry.hostname>`. The registry's NetworkPolicy admits only the Gateway's Envoy pods, and the API's admits the registry for the key fetch.

Raising `api.registry.transitKeyVersion` restarts the registry, which fetches the key again. If the registry restarted before every API replica served the new version, restart it again after the API rollout: `kubectl rollout restart deployment/<release>-registry -n <namespace>`.

