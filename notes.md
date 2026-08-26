# Loco Notes

---

## Known issues / cleanup backlog

Carried over from the Go 1.27 + dependency-bump + frontend-build work (PRs #116–#124).
None of these block anything today; they are the things we knowingly deferred.

### Frontend

- **~151 pre-existing ESLint errors.** `bun run lint:types` fails. Dominated by
  `@typescript-eslint/no-unnecessary-condition` (70), `promise-function-async` (22),
  and assorted `no-unsafe-*`. The `Web Build` CI job runs ESLint with
  `continue-on-error: true` for exactly this reason — flip it to a hard failure
  once the backlog is cleared. `bun run build` and oxlint are both clean and do gate.
- **`Home.tsx` empty states are untested in the wild.** `{true ? … : …}` had been
  short-circuiting since Feb 2026 (commit 27c2d69), so the "No Results" search state
  and the "Create Your First Resource" onboarding CTA never rendered. The original
  `filteredResources.length > 0` guard is restored, but those two states have had no
  real-world exercise — worth clicking through with an empty workspace.
- **tsgo is a dev preview.** `build`/`typecheck` use `tsgo` (`@typescript/native-preview`,
  7.0.0-dev), which is ~7x faster than tsc. `typecheck:tsc` runs the reference compiler
  in CI as a cross-check. If the two ever disagree, that step is the tripwire. Drop the
  extra step once tsgo ships stable.
- **Bundle sizes shifted slightly** when we moved npm → bun, because bun resolved some
  transitive deps differently. No `package.json` dependency changed. Only matters if we
  start tracking bundle budgets.

### Infrastructure

- **socketLB is off, and `hostServices` never worked.** `cilium.hostServices.enabled: true`
  had been dead config since Cilium 1.11 — silently ignored, confirmed by
  `bpf-lb-sock="false"` in the rendered ConfigMap. Removed it rather than flip
  `socketLB.enabled: true`, which is a real datapath change. Note prod has run without
  socket LB for the life of the cluster with no symptom, so the burden of proof is on
  turning it on. If we do, test locally first — `charts/loco-networking/values.yaml` is
  the shared base for both `local` and `prod`.
- **Cilium 1.20 moved where pod → NodePort traffic is load-balanced.** Because
  `kubeProxyReplacement: true` with SocketLB disabled, in-cluster connections to NodePort
  services are now balanced as traffic leaves the client pod rather than at the target
  node. Not a failure mode, but it changes the path and what Hubble flows look like.
  Worth a look after the first prod rollout on 1.20.
- **`bpf.tproxy: true` is incompatible with the netkit datapath.** We render
  `datapath-mode="veth"` so we are fine today, but Cilium 1.20 added
  `bpf.datapathMode: auto` — enabling it would silently revert to veth or fail to start.
  Warned inline in the values file; do not flip it casually.
- **Helm chart bumps are always-latest.** `chartbump` has no minor-only mode, so infra
  minors (cilium 1.19 → 1.20, gateway-helm 1.8 → 1.9) ride along with routine patches.
  Read upstream notes before applying to a live cluster.

### Tooling

- **`chartbump`'s README documents the opposite of what it does.** It claims the tool only
  reports; since commit `a6801fc` the bare command rewrites every `Chart.yaml` and runs
  `helm dependency update`. `-dry-run` is the preview flag. Repo: `~/Documents/chartbump`.
- **`cel-go` is pinned at v0.31.0.** v0.32.0 renamed its module path to `cel.dev/cel-go`,
  so `go get -u` correctly refuses. It reaches us transitively via protovalidate and will
  move once upstream migrates.
- **No workflow watches `.github/workflows/**`.** Changes to CI config merge without any
  check running against them — #119 merged with zero checks. Worth adding a lint/validate
  job for workflow files.

### Recently fixed (context, not TODO)

- `.gitignore` had a bare `design/` that matched `web/src/components/design/`, keeping the
  entire UI design system (11 components, 50 importers) out of the repo. The frontend could
  not build from a clean checkout or in Docker. Fixed by anchoring the pattern.
- The `BREAK_BUF` escape hatch never fired — it read `github.event.head_commit`, which only
  exists on push events. Replaced with a `break-buf` PR label. Verified working on #120.

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
  - `cmd/deploy.go` has become lost in the sauce — needs cleanup. Phase 1 done: split into
    `deploy.go` / `deploy_image.go` / `deploy_deployment.go` by concern, flags parsed upfront,
    removed a redundant duplicate `ImageTag` call in the push step.
  - Deployment should be async: CLI requests a deployment, gets back a short-lived token
    (TTL 30 min) + deployment ID tied to the request, then polls/streams.
  - **Revisit**: image tag is currently generated client-side in `buildAndPushImage`
    (`GenerateImageTag`, needs orgID/workspaceID/resourceID) — feels wrong, should move
    server-side (e.g. returned from `CreateResource`/`CreateDeployment`) so the CLI doesn't
    need those IDs just to name an image. Deferred — it's an API contract change, not cleanup.
  - Mark previous deployments as inactive before creating a new one, transactionally — already
    done server-side in `createDeploymentWithCleanup` (`api/service/resource.go`).
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

- **Cross-region failover** — DONE. Gateway-to-gateway L7 failover: each region's Envoy
  carries peer regions' public gateways as Envoy priority-1 backends, so a regional
  outage is absorbed over plain HTTPS between two public endpoints. No pod-network
  connectivity between clusters.
  - Cilium Cluster Mesh was evaluated and rejected: it re-couples failure domains,
    contradicts the "no cross-cluster traffic" and "workspace apps stay in one cluster"
    rules above, and does not improve latency. `experiments/mcs` removed.
  - See `docs/design/tdd-cross-region-failover.md` and `experiments/gateway-failover/`.
  - Still open: distinguishing a region outage from a bad deploy (failing a
    crash-looping app over just spreads it), capacity headroom in the surviving region,
    and TLS between gateways.

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
- cluster locations should be hidden as well i wanna say.
- separate repo for loco-saas?


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
