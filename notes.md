# Loco Notes

---

## V1

### Networking & Security

- **Network isolation** — TDD written. Controller creates NetworkPolicies per app namespace.
  Default deny-all, with explicit allow for envoy-gateway ingress, DNS egress, OTEL egress,
  and internet egress (excluding cluster CIDRs). Opt-in inter-app comms within same workspace
  via `AllowedPeers` on the Application CRD. Cross-workspace structurally blocked via namespace labels.
  - Shutdown cross-cluster network traffic for namespaces with `managed-by-loco`, only allow
    if `loco-workspace` matches.
  - All workspace apps must always be deployed to the same cluster — reduces network chatter,
    otherwise services can't talk to each other without egressing.

- **Secrets management**
  - Secrets need to be auto-rotated and stored in a secrets vault.
  - Create RBAC to restrict secret visibility for env vars. Kubernetes configmap of secrets
    needs to be created separately.
  - Secrets Loco manages: 
    - Terraform Cloud
    - GitLab, 
    - cloud provider (provisioning),
    - GH OAuth client secret
    - Cloudflare API token (cert-manager)
    - Grafana root password?

- **GitHub OAuth token longevity** — reduce token lifetime, currently too long-lived.

- **TLS in-cluster** — do we need mTLS for in-cluster communication? Needs an explicit decision.

- **Loco root password** — needs to be auto-rotated.

### Observability

Basic logs and metrics are working via otel + clickhouse. Still needed:

- **Multi-tenancy attributes** — all logs/traces/metrics must include org-id, workspace-id,
  app-id, app-name on every record. Needed for proper tenant isolation in queries.
  - On successful routing, add `loco-tenant-id` header so it can be pulled later in OTEL
    for dashboarding.

- **ClickHouse schema** — current schema is auto-generated. Need custom schema with indexes
  on app-id and workspace-id (queries are slow without). TTL policy per tenant.
  - Potential SQL injection with the limits + query parameters — needs fixing.
  - Validate ClickHouse resource allocation (750MB may not be enough).
  - Move ClickHouse monitoring to admin dashboard only.
  - Parse severity/level out of structured logs.
  - Show all fields, not just the body.
  - Support ascending/descending timestamp order, substring filtering, arbitrary filters.
  - Tons of metrics currently being exported — optimize what's sent, do this when revisiting
    table structures.
  - Things that need a TTL: configmaps for apps/deployments, data in ClickHouse, audit events.

- **OTEL pipeline optimization** — reduce cardinality, drop unnecessary/high-cardinality
  metrics in processors. Only collect logs from loco-managed resources.

- **Dashboards** — build workspace and per-service dashboards in the obs tab on the UI.
  Organization/different streams, segregated by workspace/project scope. Grafana as an
  optional export later.
  - Disk metrics currently missing.
  - Update system design diagram to represent observability.

- **Data lifecycle** — TTL-based cleanup per tenant. When a user deletes a workspace or app,
  kick off deletion of all associated logs/metrics/traces immediately — save absolutely nothing.
  - How do we run cleanups? Consider a Kubernetes CronJob, but account for cluster crashes
    during cleanup.

- **Log tailing** — live tail comes directly from the cluster; historical logs from ClickHouse.
  CLI table should support a simple freeze/pause.

- **Tracing** — pushed to V2. Railway/Heroku don't support tracing either, so not urgent.

### Deploy & Builders

- **Non-interactive deploy** — `loco deploy --non-interactive --token {TOKEN}`. Needed for CI.
  Dependent on TVM being stable.

- **Image builders** — TDD written. Accept `--docker-socket` / `LOCO__DOCKER_SOCKET` to support
  any OCI-compatible runtime (Podman, nerdctl, etc.) via Docker-compatible socket API. Version
  check branches on `Platform.Name` — only enforced for Docker Engine. Auto-detect socket if
  not provided.
  - Docker preflight check (socket existence + daemon ping) already implemented.
  - `--image` flag already implemented for skipping local build.
  - Image IDs for now can only be from public container registries like GHCR.
  - Still need: validate image is safe, enforce max image size (1GB cap already done).
  - Respect `.dockerignore` / `.gitignore` when building container images.
  - Better API verification on builds — size, registry, etc.

- **Container registry** — currently using GitLab registry.
  - Set lifecycle policy (last 2 images per resource, 6-month max).
  - Require image prefixing with random hash.
  - Only allow registry writes from Loco infra, not reads.
  - Set max Docker image size (cluster limited).
  - GitLab registry token is only fetched at deploy time — if a new node pulls the image
    later, the token is expired (5 min TTL). Need continuous rotation tied to the image
    pull secret in the app namespace.
  - Would be better to deploy our own registry via Harbor or similar (V2).

- **Deployment flow**
  - `cmd/deploy.go` has become lost in the sauce — needs cleanup.
  - Deployment should be async: CLI requests a deployment, gets back a short-lived token
    (TTL 30 min) + deployment ID tied to the request, then polls/streams.
  - Image tag is currently built on the CLI — feels wrong.
  - Mark previous deployments as inactive before creating a new one, transactionally.
  - Cleanup partial resources if deployment fails at any step — simple implementation done.
  - `loco deploy` should be all-or-nothing per region. Controller should also be all-or-nothing.
  - Max helm history at 5, remove old helm secrets.
  - `max_concurrent_app_deployments` — some sort of env for configuring deployment behavior.
  - For rolling back, we need to persist the env somewhere — cannot persist in Postgres.
  - Does loco need to store the local path the user deployed their app from? Warn if path
    has changed. Store mapping under `$HOME/.loco`.
  - missing a proper deployment interface for what's happening inside allocateResources —
    need a simple way to start, execute, and watch these changes.
  - The API needs to take in config of `map[string]any` and use it upstream to build the app.

- **GRPC support** — believe current Envoy Gateway setup allows GRPC passthrough, but needs
  explicit verification.

- **loco.toml** — respect more of the config. Deploy settings like regions, rollback settings,
  pre/post deploy scripts.

### API & Data Model

- **Never return raw DB errors to the client** — wrap and return a generic message only.
  Set up proper `errors.Is()` for pgx/pq error handling; a package exists for this.

- **Request validation** — add buf validate rules across all endpoints (some are missing).

- **App config versioning** — resource spec needs a schema version stored in the DB.
  `configToResourceSpec` already takes a `version` param but the DB doesn't persist it.
  `create loco resource` will need to handle loco spec versions.

- **API design review** — some contracts feel off. For CRUD, return only the ID, not the
  full resource. Potentially loco-api chats with loco-controller eventually via controller-runtime.

- **ResourceSpec** — current spec is service-only. Needs to be typed per resource type
  when we add DB, cache, blob. "what is this locoresourcespec man."

- **SQL hygiene**
  - Unique constraint checks should exclude the current row's ID on updates.
  - `ORDER BY created_at` in many queries with no index — add indexes wherever we sort.
  - Multi-step writes need to be wrapped in transactions.

- **Owner references** — should we be using k8s owner references more?

- **Environments** — need to evaluate the handling of environments on both UI and backend.

- **`use.go`** — should eventually be able to switch between different scopes, list all
  scopes and switch between them interactively.

- **Remove `host` from persistent flags.**

### Infrastructure & Multi-cluster

- **Multi-cluster** — two sub-problems:
  1. *Cluster administration*: syncing CRDs and helm chart versions, verifying health.
     Tentatively: `kubectl apply` CRDs first, then helm upgrade. FluxCD is a candidate.
     How do we ensure changes have rolled out and things are in sync?
  2. *Placement*: a placement API that picks a cluster based on region, current resource
     utilization, and environment (prod vs non-prod).
  - Is the control-plane itself a k8s cluster? How do clusters chat with one another?
  - Clusters need region/env tags, possibly taints/tolerations.

- **Certificate management** — should certs be created and managed in the region they're
  deployed? This should technically be a one-time process. Potential fix: a designated
  "leader" cluster per region that manages certs.

- **Helm charts** — parametrize everything; no hardcoded values. Remove CRDs from helm chart —
  CRDs must be installed explicitly and separately. Using FluxCD for this.
  - Helm charts for `loco-core` need to be separated further.
  - ClickHouse is named weirdly, and so is our controller.

- **HPA for nodes** — configure a proper cluster autoscaler / node HPA.

- **Envoy Gateway scaling** — default Envoy deployment has no HPA attached.
  Need a full load test on loco and its services.

- **Evaluate ArgoCD** and others for better CD of Kubernetes resources.

- **Cilium** — evaluate whether `cilium-envoy` can be trimmed. Potentially use vtprotobuf
  — but development has stalled, no editions support:
  https://github.com/planetscale/vtprotobuf/commits/main/

- **Rate limiting** 
    - use the out-of-box Envoy rate limiter to implement some default rate limiting.
    - ensure that the deploy endpoint is protected well?
    - eventually extend rate limiting abilities to tenant apps.

- **Dependency chart** — create a full map of all Loco dependencies broken down by component.
  Keep it in sync so we always know what breaks when something changes.

- **Resource management evaluation** — how many resources are we using? What are we wasting?
  Run loco with as little resources as possible.

### Platform Services

- **Invitations service** — needed before public launch.
- **Emailing service** — tied to invitations and notifications.
- **Billing service** — needed for V1 monetization.
- **Notifications service** — in-app + email. Generic webhook for notifying admins on failures.
- **Build logs microservice** — separate from application logs; build output should be
  streamed and stored independently.

### Testing

- API: unit tests + integration tests (real DB, no mocks).
- CLI: unit tests for core deploy logic. Deployment scripts need tests.
- Controller: unit tests + e2e with kind.
- Load testing: initial benchmarking before public launch.

### Data & Lifecycle

- Full deletion on user/app/workspace deletion — logs, metrics, secrets, k8s resources,
  registry images. Save absolutely nothing.
- Postgres backups.
- ClickHouse backups.

### Docs

- API docs already generated from proto definitions.
- Use Zensical for public-facing docs.

---

## V2

### Observability

- **Tracing** — OTEL traces already flowing into ClickHouse. Need UI, per-tenant isolation,
  and region/env attributes on trace data.
- **Grafana programmatic dashboards** — use `grafana-openapi-client-go` to provision
  per-workspace dashboards on workspace creation. Eventually add email alerts.
- **ClickHouse cloud** — potentially, but still need TTLs and custom table setups.

### Security & Networking

- **Docker image scanning** — TDD exists. Scan on push using Trivy or Harbor's built-in scanner.
- **Custom container registry** — Harbor or Quay, with tag-prefix/name-prefix access controls,
  multi-tenancy, integrated scanning. Artifact attestations eventually. Civo offers this.
- **Egress control** — allow users to restrict or allow specific external egress per app.

### Deploy & Builders

- **Custom domains** — user brings their own domain; Loco provisions cert-manager certificate.
- **Non-HTTP health checks** — allow bash-based or exec-based health checks.
- **App sleep mode** — auto-sleep after N days of no traffic. Wake on request via path rewrite
  to `/revive-app?app-name=foobar123&og_url=...`, then redirect back. Who sleeps the app, who
  rebuilds it?
- **Loco Packages** — bundle of services always deployed together to one workspace.
  - `loco deploy -r` for recursive discovery and deployment.
  - One-click deletes for the whole package.
  - Maybe deploy to an existing workspace.

### Infrastructure

- **Resurrector** — deployed outside the cluster. Takes hourly snapshots (etcd or Postgres)
  and can bring up exactly one cluster from scratch.
- **Cluster snapshots** — etcd snapshots or Postgres-based.
- **Certificate management** — full multi-cluster cert strategy using a per-region leader cluster.
- **Infra patch management** — map all Loco dependencies (Envoy Gateway, Cilium, cert-manager,
  OTEL, ClickHouse, ...) and define an update/patching strategy per component. May need
  blue-green deployments for Kubernetes node patches. Auto-managed for fargate-like providers,
  manual for self-managed nodes.
- **Profiles** — user profiles / bring-your-own-cloud config.

### Platform

- **Admin dashboard** — deployed apps count, active requests, per-tenant resource usage.
  Potentially use the Kubernetes dashboard for the infra view. There is value for those
  planning to bring your own cloud, but need to figure out keys and roles.
- **Status page** — `status.loco.build`. API latency + uptime (last 24h), builder queue
  backlog, average deploy duration, degraded regions, current incidents (auto-created from
  Prometheus/Grafana alerts). When multi-cluster: cluster-specific status too.
- **Audit/events table** — record all mutating operations per org.
- **UI testing** — Playwright only, no unit tests for UI. `toast.error()` on mutations
  instead of putting errors in a card.
- **Different resource types** — DB (Postgres), cache (Redis), blob (S3-compatible).
- **Dedicated per-service disks** — persistent volumes per app.
- **Umami** — potentially set up for frontend analytics. Loco backend API and umami should
  be configurable from the UI.
- **Interactivity during login** — introduce interactive login flow.
- **`loco sync`** — CLI command that diffs local `loco.toml` against what's deployed and
  shows a nice diff on both CLI and UI.
- **Secrets integration** — pull from AWS SSM, Vault, etc. Too much for MVP. Users can
  technically do this themselves via their container but getting the initial secret in is
  the hard part.

### Data Model

- **Normal IDs instead of UUIDs** — simpler code, cheaper, naturally sortable. Switch
  when doing the next major schema migration.
- **Split sqlc queries into separate packages.**
- **Efficient ordering** — index `created_at` wherever we sort by it.
- **Lack of auditing** — need an audit table or events recording.
- **Inefficient unique checks** — unique constraint checks should exclude the current row's ID.

- **Rate limiting per tenant** — more granular rate limiting beyond just the Envoy global limiter.
---

## V3

- **Account hygiene** — background process to clean up inactive accounts, release domains,
  remove unused resources. Ensure people are actually using the account, not just creating
  it and leaving stuff there.
- **Canary deployments** — for Loco's own services first, then expose to users.
- **Kubernetes export** — `loco export` converts `loco.toml` to Kubernetes YAML. Escape hatch
  for users who want to self-host or graduate off Loco.
- **Graduating services** — a formal path for users to graduate from Loco to self-managed infra.
    - perhaps just a way to download their YAMLs

---

## Backlog (only if users ask for it)

- `loco init --minimal` flag — currently too chunky.
- `NO_COLOR` / `--no-color` support, disable colored rendering and fancy UTF-8 characters.
- Non-Docker build tools (podman, buildah, nerdctl) — socket-based support is partially
  addressed in the image-builder TDD; full buildah/buildkit support is separate.
- `vtprotobuf` for proto serialization — stalled, no editions support yet.
- Evaluate `controller-runtime` for loco-api ↔ loco-controller communication.

---

## Philosophy

- Reduce package depth.
- Stick to google, k8s, go-ecosystem packages — minimize external attack vectors.
- Avoid outside packages where a stdlib or well-known alternative exists.
- Define a process for patching security vulnerabilities in dependencies.
