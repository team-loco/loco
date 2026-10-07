# Loco Notes

---

## Known issues / cleanup backlog

Carried over from the Go 1.27 + dependency-bump + frontend-build work (PRs #116–#124).
None of these block anything today; they are the things we knowingly deferred.

### Frontend

- **`components/ui/*` is vendored, `components/design/*` is ours.** `ui/` is whatever the
  shadcn CLI generates and must stay overwritable; project styling lives in `design/`,
  which wraps the `ui/` component and layers overrides through `cn()`. App code imports the
  wrapper, never the vendored component. This is enforced: `eslint.config.js` restricts the
  whole `@/components/ui/*` pattern, exempting only `components/ui/**` (shadcn components
  legitimately compose each other) and `components/design/**`. There is no allowlist to
  maintain — **every** `ui/` module needs a `design/` wrapper before app code can import it,
  so adding a shadcn component forces a deliberate choice about whether it needs styling.
  Most wrappers are a one-line `export *`; that is the correct "no override yet" state.
  - The wrappers are deliberately **not** all re-exported from `design/index.ts`.
    `web/package.json` sets no `sideEffects`, so an `export *` barrel spanning `Chart`
    (recharts, 293 KB) and `CodeBlock` (shiki, lazy-loaded) risks pulling them into every
    chunk that imports anything from `design/`. Import the module directly
    (`@/components/design/Table`); the barrel stays limited to the lightweight primitives.
  - Before Sept 2026 this did not hold: `Button`, `Badge`, `Input` and `Card` imported
    `@base-ui/react` directly and re-declared the whole `cva` from scratch, so a shadcn
    update to `ui/button.tsx` had no effect on `design/Button.tsx`. They are real wrappers
    now.
  - `design/Badge.tsx` carries a status palette (`success`/`warning`/`error`/`running`/
    `pending`/`stopped`) in **hardcoded hex**, not theme tokens. It predates the change and
    was preserved verbatim rather than retinted. Worth moving into `index.css` variables.
- **`Home.tsx` had a second dead empty state, and it was dead for a different reason.**
  The `{true ? … : …}` short-circuit (commit 27c2d69) was fixed earlier, but restoring the
  `filteredResources.length > 0` guard only exposed that `searchTerm` was declared
  `const [searchTerm] = useState("")` — destructured with no setter, so it could never be
  anything but `""`. `filteredResources` was therefore always `allResources` and the
  "No Results" branch was unreachable. It was also redundant: `BentoDashboard` owns the
  resource search now and has its own empty states. The dead state, filter and branch are
  gone. The surviving "Create Your First Resource" CTA is confirmed rendering against a
  real empty workspace.
- **A stale `node_modules` produces ~140 phantom type errors.** `bun add`/`bun remove`
  do not always prune `node_modules/.bun`, so a second copy of `@tanstack/query-core` can
  linger after an update even though `bun.lock` references only one. Two copies means two
  structurally incompatible `QueryClient` types and every `connect-query` hook fails to
  typecheck. If you see `Property '#private' in type 'QueryClient' refers to a different
  member`, the fix is `rm -rf node_modules */node_modules && bun install` — not a code
  change. `eslint --cache` holds onto the bad results afterwards; delete `.eslintcache` too.
- **tsgo is a dev preview, and TypeScript 7 is blocked by typescript-eslint.**
  `build`/`typecheck` use `tsgo` (`@typescript/native-preview`, 7.0.0-dev), ~7x faster than
  tsc; `typecheck:tsc` runs the reference compiler in CI as a cross-check.
  - `typescript@7` is now stable **and is itself the native compiler** — it ships
    platform binaries (`@typescript/typescript-darwin-arm64` and friends) and exposes them
    as `tsc`. So the real TS 7 migration is a consolidation: drop
    `@typescript/native-preview`, drop the `typecheck:tsc` cross-check, point `build` at
    native `tsc`, and the dev-preview caveat disappears.
  - It cannot be done yet. `typescript-eslint@8.71.0` declares
    `typescript: ">=4.8.4 <6.1.0"` and hard-errors on TS 7 — *"typescript-eslint does not
    support TS 7.0"* — which would kill the entire type-aware rule set in
    `eslint.config.js`. Support is tracked for TS >= 7.1 in
    typescript-eslint#10940. **Recheck that issue before attempting the upgrade**; keep
    `typescript` pinned to `~6.0.x` until it closes.
- **Dependencies are otherwise current as of Oct 2026.** The two remaining major upgrades
  deferred earlier now have PRs of their own: `motion` 12 → 13 and
  `@tanstack/react-table` 8 → 9. Note that react-table v9 ships an official
  `migrate-v8-to-v9` skill inside the package — read it before touching table code.
- **Mock data still ships in a live page.** `pages/resource-details/mock-usage.ts` feeds the
  per-region CPU/memory bars on `ResourceDetails`; the numbers are deterministic noise
  seeded off status, not telemetry. `pages/Usage.tsx` and `pages/Resources.tsx` are
  "Coming soon" cards. All three are waiting on the obs pipeline.
- **Four font stacks load at once**, which is deliberate but worth knowing: Satoshi
  (self-hosted `.woff`, set on `body`), Geist Variable (`@fontsource-variable/geist`, the
  `--font-sans`/`font-sans` token), and Google-hosted JetBrains Mono (`--font-mono`) and
  Raleway (`--font-serif`). The Google ones are render-blocking `<link>`s in `index.html`.
  Satoshi is `.woff`, not `.woff2` — converting would save roughly 30%.
- **`public/` is served verbatim and is not tree-shaken.** Anything dropped in there ships
  in the nginx image whether or not a component references it. `gradient3.svg` (1.3 MB) and
  `logo.png` (825 KB) are both referenced and both unoptimised.

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
  check running against them — #119 merged with zero checks. This is not theoretical: it
  let every image-build job in both deploy workflows sit broken (see "Recently fixed"), and
  let a fully commented-out `controller-e2e.yml` fail on every single push for months.
  `actionlint` catches all three classes in under a second; it should be a job.

### Recently fixed (context, not TODO)

- **Toasts never followed the theme.** `ui/sonner.tsx` read `useTheme` from `next-themes`,
  but the app has always used its own `ThemeProvider` (`lib/theme-provider.tsx`). With no
  next-themes provider mounted, `useTheme()` returned its default and the Toaster was
  pinned to `theme="system"` — so toggling the app to dark on a light-mode OS gave light
  toasts over a dark UI. Now reads `@/lib/use-theme`; `next-themes` is gone. (This is the
  "fix sonner nonsense" line in `web/notes.md`.)
- **Both animation plugins were loaded.** `index.css` had `@import "tw-animate-css"` *and*
  `@plugin "tailwindcss-animate"` — the Tailwind v4 package and the deprecated v3 one it
  replaces, defining the same utilities twice. Dropping the v3 plugin removed 8 KB of
  duplicate CSS with every `animate-*`/`fade-*`/`slide-*` utility verified still present.
- **The postcss toolchain was inert.** `postcss`, `autoprefixer` and `@tailwindcss/postcss`
  were installed with no `postcss.config.*` anywhere; Tailwind v4 runs through
  `@tailwindcss/vite`. Removing all three produced a byte-identical CSS bundle.
- **`cn` now comes from the `cn` package** rather than `twMerge(clsx(...))`. `lib/utils.ts`
  re-exports it, so every call site still imports from `@/lib/utils` and nothing was
  rewritten. `clsx` and `tailwind-merge` are gone. Checked before adopting: the npm name
  was a 2013 Chuck Norris CLI transferred to the `shadcn` account, the current package has
  npm provenance attestations from `shadcn-ui/cn`, zero runtime deps, MIT. It is 0.4.0 and
  moving fast — worth re-checking if class merging ever behaves oddly.
- **~1,430 lines of dead components deleted** — `nav-user`, `section-cards`, `search-form`,
  `AppCard`, `Layout`, `dashboard/{ApplicationsGrid,ApplicationsTable,RecentDeployments,
  OrgFilter,AppSearch}` and `lib/mock-metrics`, none of which had an importer.
- **~11 MB of unreferenced assets deleted** from `public/` — chiefly a 9.6 MB
  `landscape.jpg` that nothing pointed at (`landscape.webp`, 76 KB, is what renders), plus
  `landscape1.jpg` and an unused `space-grotesk` font family. `dist` went 16 MB → 5.6 MB.
  The favicon was a hotlink to `openmoji.org`; it now serves the local `train.svg`.
- **Every image-build job in both deploy workflows was invalid.** All four called the
  reusable `build-push.yml` from inside `steps:` (`uses:` at step level only resolves
  actions, not workflows), so controller/api/ui/agent images could never build. Moved to
  job-level `uses:`. The same jobs gated on
  `contains(github.event.head_commit.modified, 'web/')` — `modified` is an array of exact
  paths, so `contains` does element equality and never matched, and it only covers the last
  commit of a push. Replaced with a `changes` job that diffs the push range.
- **The UI image was built with development config.** `web/Dockerfile` declares
  `ARG VITE_API_URL`/`VITE_APP_ENV`, but nothing ever passed them and `build-push.yml` had
  no input for them, so production images baked in `http://localhost:8000` and
  `APP_ENV=DEVELOPMENT` (which also forces the JSON wire format instead of binary).
  `build-push.yml` now takes `build_args`; the deploy workflows pass
  `vars.VITE_API_URL_{STAGING,PRODUCTION}` with a fallback to `https://api.loco.build`.
  **Set those two repository variables** if the hosts ever diverge — today both
  `clusters/*/infrastructure/values/loco-core-values.yaml` point at the same API host.
- **`controller-e2e.yml` was entirely commented out**, so GitHub parsed a workflow with no
  `on:`/`jobs:` and recorded a failed 0s run on every push. Deleted.
- **The SPA shell was cacheable.** nginx set `immutable` 1y on hashed assets but no
  `Cache-Control` at all on `index.html`, so a heuristically-cached shell could outlive the
  chunks it references and 404 them after a deploy. Now `no-cache`.
- **Dead config references removed**: `tsconfig.node.json` included a `tailwind.config.ts`
  that does not exist (Tailwind v4 is CSS-first) and `components.json` pointed at it too;
  `tsconfig.app.json` targeted `es2024` with `lib: ES2022`. `@bufbuild/buf` and `shadcn`
  were runtime `dependencies` of the web app — buf moved to root devDependencies (it is a
  repo-level codegen tool; note `make gen` actually calls a PATH `buf`, not this one),
  shadcn to web devDependencies.
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
    - the registry's push and pull credentials
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

- **Image builders** — `loco deploy` packs the directory that holds loco.toml (respecting
  `.dockerignore`), uploads it to the source bucket, and the build controller builds it with
  rootless BuildKit in a Job on a cluster with `builds.enabled`. Users never build or push
  images themselves. `--image` deploys a public image without a build.
  - Image IDs passed to `--image` can only come from public registries like GHCR.
  - Still need: validate image is safe.

- **Container registry** — one central zot registry (on Railway in production, from
  `compose.yaml` locally). Only build pods push, with the `loco-registry-push` credential;
  nodes pull with a long-lived pull secret that the Application controller copies into each
  workspace namespace. Deployments pin images by digest.
  - Set lifecycle policy (last 2 images per resource, 6-month max).
  - Set max Docker image size (cluster limited).

- **Deployment flow**
  - `cmd/deploy.go` has become lost in the sauce — needs cleanup. Phase 1 done: split into
    `deploy.go` / `deploy_image.go` / `deploy_deployment.go` by concern, flags parsed upfront,
    removed a redundant duplicate `ImageTag` call in the push step.
  - Deployment should be async: CLI requests a deployment, gets back a short-lived token
    (TTL 30 min) + deployment ID tied to the request, then polls/streams.
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

- **Cross-region failover** — approach decided, implementation separate. Gateway-to-gateway
  L7 failover: each region's Envoy carries peer regions' public gateways as Envoy
  priority-1 backends, so a regional outage is absorbed over HTTPS between two public
  endpoints, with no pod-network connectivity between clusters.
  - Cilium Cluster Mesh was evaluated and rejected: it re-couples failure domains,
    contradicts the "no cross-cluster traffic" and "workspace apps stay in one cluster"
    rules above, and does not improve latency. `experiments/mcs` kept for reference.
  - Mechanism proven end to end in `experiments/gateway-failover/`; rationale and the
    sharp edges in `docs/tdd/cross-region-failover.md`.
  - Open before it is production-viable: TLS between gateways (peers default to 443 and
    nothing configures client TLS yet), distinguishing a region outage from a bad deploy,
    and capacity headroom in the surviving region.

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
  - Mostly a chart change: obs-proxy already reads `CLICKHOUSE_URL` from env. See
    [`docs/design/tdd-pluggable-dependencies.md`](docs/design/tdd-pluggable-dependencies.md).

### Security & Networking

- **Docker image scanning** — TDD exists. Scan on push using Trivy or Harbor's built-in scanner.
- **Custom container registry** — Harbor or Quay, with tag-prefix/name-prefix access controls,
  multi-tenancy, integrated scanning. Artifact attestations eventually. Civo offers this.
  - Superseded by [`docs/design/tdd-pluggable-dependencies.md`](docs/design/tdd-pluggable-dependencies.md),
    which argues for zot over Harbor on footprint grounds.
- **Egress control** — allow users to restrict or allow specific external egress per app.

### Deploy & Builders

- **Custom domains** — user brings their own domain; Loco provisions cert-manager certificate.
- **Build cluster** — builds never run on the control plane: it holds every secret, and builds
  run untrusted code with relaxed seccomp. Today they run on worker clusters with
  `builds.enabled` in the loco-operator chart, so a single cluster both builds and runs apps.
  The goal is a dedicated build-only cluster (builds on, no apps) with builds off on every app
  cluster, the same split as the separate build fleets of Railway, Heroku and Render.
  - Missing piece: an "accepts apps" switch on clusters, so deployments never land on the
    build-only cluster. Deployment placement today picks clusters by region and tier only.
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
- **Kubernetes compatibility matrix** — Loco runs on clusters we do not upgrade in lockstep,
  so the controller and agent must work across several Kubernetes minors at once. Decide the
  supported range (e.g. the three newest minors), run the controller and agent e2e suites
  against a kind node image for each minor in CI (`kind create cluster --image`), and publish
  the matrix. kind's default node image and kubectl in `mise.toml` are the version we develop
  against, not the only one we support.

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



- unanswered

-- secrets
    - how do we handle managing secrets for our app?
    - openbao
-- docker images
    - i think we need to host our own? harbor? the complexity is growing insanely.
    - do we just insert the image into the current cluster?
        - or all clusters/registries
    - like what happens if a whole cluster goes down or something, and we need to grab the docker image again
    - still ties into secrets managent
    - PARTLY ANSWERED in docs/design/tdd-pluggable-dependencies.md: keep the blob store
      external so images survive cluster loss. The one-registry-per-region vs
      global-with-pull-through question is still open.
-- networking
-- perhaps env variables, settings, are actually part of the app information, just copied over in a deployment or something.
