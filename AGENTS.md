# Agent Guidelines

## Overview

Loco is a container orchestration platform that simplifies application deployment. Loco's goal is to be an OSS competitor to Vercel and Railway.

Loco turns application configuration into Kubernetes resources. Users deploy source or container images; the control plane records desired state, cluster agents apply `Application` CRs, and controllers reconcile workloads, services and routing. Loco handles workspace network isolation through NetworkPolicy, ingress through Envoy Gateway, TLS certificates through cert-manager, and logs, metrics and traces through OpenTelemetry.

Clients can use infrastructure as code through `loco.yaml`: a declarative file describing services, images or source builds, regions, sizing, domains and environment overrides. `loco infra plan/apply` previews and applies changes; `loco deploy` also builds source services. See [the field reference](docs/content/deployment/loco-yaml.md).

## Definitions

- **Organization**: Billing boundary; owns workspaces and organization membership.
- **Workspace**: Network boundary; groups related resources and environments, with its own membership and network isolation from other workspaces.
- **Environment**: An isolated deployment target within a workspace, such as staging or production, with its own configuration, secrets and deployment history.
- **Resource**: A named application component in a workspace, such as a web service or worker; can run in multiple environments and regions.
- **Build**: A source-to-container-image job; its output image can be deployed to environments.
- **Deployment**: An immutable runtime configuration snapshot for one resource in one environment and region, with rollout status.
- **Domain**: A hostname assigned to a resource for public routing.

## Architecture

Loco separates the control plane (platform state and APIs) from the data plane (worker clusters running applications and builds).

### Clients

- **`cmd/`**: Two Go CLIs running on client/operator machines, using Cobra + Charm (Fang, Lipgloss; Bubble Tea for `loco`'s interactive UI):
  - `loco`: For clients connecting to Loco: deploy applications and manage resources, environments, workspaces and organizations.
  - `locoadm`: For administrators running their own Loco installation: bootstrap the first admin, manage clusters and users.

### Control plane (`charts/loco-control-plane/`)

- **`api/`**: Go API for platform state, authentication, deployment orchestration and agent sync; ConnectRPC v2 (`connectrpc.com/connect/v2`) and PostgreSQL.
- **`web/`**: React dashboard for application teams to manage applications and inspect logs, metrics and traces; calls the control-plane API.
- **`observability-proxy/`**: Separate ingest and query deployments: ingest writes OpenTelemetry logs, metrics and traces to ClickHouse; query reads them, serves live log tails and runs ClickHouse schema migrations.

### Data plane (`charts/loco-worker/`)

- **`agent/`**: In-cluster bridge to the control plane: apply desired applications and builds, report runtime status, and renew telemetry credentials.
- **`controller/`**: Kubebuilder operators: reconcile `Application` CRs into workloads, services, routing and network policies; reconcile `Build` CRs into BuildKit jobs.
- **`builder/`**: Runs inside build jobs: fetch source, restore build cache, and push built images and cache to the registry. BuildKit builds the images.
- Application workloads run here, alongside Envoy Gateway for ingress, cert-manager for certificates and OpenTelemetry collectors exporting to the control plane.
- **Each environment runs on its own clusters.** Staging and production never share a cluster; a resource has at most one placement per cluster.

### Shared code and tooling

- **`k8sapi/`**: Shared Kubernetes API types and validation for `Application` and `Build` CRs, plus resource labels.
- **`proto/loco/`**, **`gen/`**: Protobuf service definitions and generated Go/TypeScript types and clients; do not edit generated code directly.
- **`internal/`**: Shared Go packages, including `loco.yaml` parsing and validation, RPC clients, authentication and session helpers. CLI-only packages live in `cmd/internal/`.
- **`charts/`**: Helm charts for the control plane and worker clusters.
- **`tests/e2e/`**: End-to-end suites that build and exercise Loco on kind clusters.
- **`tools/`**, **`mise-tasks/`**: Go development/release tools and mise task scripts for builds, generation, checks and local environments.
- **`docs/content/`**, **`schemas/`**: User documentation and editor JSON Schemas, including `loco.yaml`.

The root Go module holds only `internal/`, `gen/` and `tools/`; CLI-only dependencies belong in `cmd/go.mod`.

## Build/Lint/Test Commands

Builds, tests, code generation, linting and the local environment are mise tasks: `mise tasks` lists them, `mise run <task>` runs one. Every tool (go, bun, golangci-lint, buf, sqlc, helm, kind, ...) is pinned in `mise.toml` and `mise.lock`, and CI installs the same versions; `mise run setup` installs them, the web dependencies and the git hooks. `controller/Makefile` is the kubebuilder scaffold and stays; the root tasks call into it. Add a task to `mise.toml` (or a script under `mise-tasks/`) for commands you find yourself running often.

## Principles

- **Every change has a test** that fails before it and passes after it.
- **Own regressions.** A test that fails after your change is yours to diagnose; don't argue it was already broken without proof.
- **No assumptions.** Before implementing, state the assumptions the design rests on, and
  check each against the code, the docs or a test. If one can't be checked, or there is
  more than one reasonable reading of the request, stop and ask instead of picking one
  silently. This covers runtime assumptions most of all: what exists before what, event
  ordering, cache warmth, a single replica or a single region. A design that only works
  while one of those holds must either enforce it or survive its failure, and the PR names it.
- **Push back.** If a simpler approach exists, or the request rests on something untrue, say
  so before writing code.
- **Simplicity first.** The least code that solves the problem: no features, abstractions,
  configurability or error handling for cases nobody asked for or that can't happen. If
  200 lines could be 50, rewrite it.
- **Define done before starting.** Turn the task into checks that prove it: "fix the bug" is
  a test that reproduces it, "refactor X" is the suite passing before and after. For
  multi-step work, write the plan as steps, each with the check that verifies it.
- **Evidence over assumptions.** Reproduce a bug before fixing it, and find the root cause instead of patching the symptom.
- **Find files before reading them.** File and symbol names in issues, reviews and old notes
  go stale. Locate them with `rg --files | rg <name>` or `rg <symbol>` before opening one.
- **Fail fast on invalid states.** Assert invariants; don't hide defects behind defensive conditionals.
- **Clean as you go.** In the same PR, fix stale code in the files your change edits
  (unread fields, outdated comments, dead functions) and remove anything your change leaves
  unused, including whole files. Stale code in files you don't edit goes in an issue.
- **Features ship with an e2e suite**: one `mise run e2e:<suite>` that builds its own kind
  cluster from a clean machine and cleans up after itself. Extend `tests/e2e/`.
- **Run e2e sparingly.** Unit and DB tests come first. Locally, run only the suite your change
  affects, against a kept cluster (`--no-teardown`, then rerun with `--skip-build`); the PR's CI
  is the from-scratch run. A setup failure is a harness bug to log and fix, not a reason to
  loop reruns.

## Dependencies and Configuration

- **Every version is pinned in a file Renovate tracks** (go.mod, package.json, mise.toml, chart
  values, Dockerfiles, workflows). Never write a version in code or a script; read it from the
  tracked file. New pins start at the latest release. Container images carry a tag and a
  digest; a version a Dockerfile or Makefile pins outside a `FROM` line takes a
  `# renovate: datasource=... depName=...` line above it. `mise run lint:versions` checks the
  images, and that docs/pyproject.toml requires the Python minor version mise.toml pins.
- **A build reports the version it was tagged as.** Every Go binary reads it from
  `-ldflags "-X main.version=..."`, which each Dockerfile fills from the `VERSION` build arg.
  `build-push.yml` passes the image tag (`sha-<commit>`), the CLI release its git tag
  (`vX.Y.Z`), and e2e `e2e`, the tag of its images. Without one, a binary reports its Go
  module version, `(devel)` inside Docker. The UI build reads the same `VERSION` build arg and
  serves it as `/version.json` and a `loco-version` meta tag, `(devel)` without one. The charts
  have no default tag for Loco images.
- **One source per value.** Don't repeat a chart value as a Go default or a script constant.
- **Read configuration once at startup** into a config struct, like `newAPIConfig()` in
  `api/config.go`, with the typed env readers in `api/env.go`; no `os.Getenv` elsewhere. Invalid configuration panics. Tunable limits,
  timeouts and defaults are config fields, not literals in the code.

## Code Style

Load the user-level `go-review` skill before writing or reviewing Go.

- Write new tooling in Go under `tools/`, with tests; existing Python tooling can stay.

## ConnectRPC traps

- Set response headers before calling the next interceptor: unary responses are
  written inside that call, so headers set afterward are lost. Server interceptors
  run before decoding; keep authentication at that boundary.
- Config and Agent stay outside the authenticated server. Preserve that interceptor
  boundary, and include both servers when deriving reflection's service list.
- Connect scrubs plain handler errors and errors forwarded from another RPC. Map
  forwarded errors explicitly. `rpcerr.New(code, err)` exposes `err.Error()` to the
  client; use explicit public messages with `WithCause` for private diagnostics.
- The 4 MiB per-message read limit is deliberate. Do not add
  `connecthttp.WithReadMaxBytes(0)` to work around a rejected request.

## Repo Hygiene

Read [CLAUDE.local.md](CLAUDE.local.md) when present for this checkout's Git and PR workflow.

## Generated Code

Do not edit generated files directly. Regenerate after changing their sources:

- Protobuf definitions, SQL queries or migrations: `mise run gen`.
- Kubernetes API types or controller markers: `mise run controller:gen` (deep copies, CRDs, RBAC and chart copies).

Review generated diffs; CI checks for drift.

## Charts

Read [charts/AGENTS.md](charts/AGENTS.md) before chart work.

## loco.yaml

- **Types and validation**: `internal/locofile` (`types.go`, `validate.go`, `merge.go` for
  environment overrides). `schemas/loco.v1.json` is the JSON Schema editors load;
  `TestSchemaMatchesTypes` reflects over the types and fails when the schema drifts.
- **Commands**: `loco infra init|validate|plan|apply|transfer` and `loco deploy`. A file belongs to
  one `partial`; `transfer` moves a service between partials. `deploy` builds the source services
  once and calls `PlanService.Apply`.
- **Domains**: `domains` lists full hostnames under a platform domain a cluster reports; without it the
  service is private. The platform domain comes from the API, never from the CLI.
- **Defaults and limits**: the API owns them (`GetConfig`, plan validation); the CLI hard-codes none.
- **Reference**: `docs/content/deployment/loco-yaml.md` documents every field.

## Frontend

Read [web/AGENTS.md](web/AGENTS.md) before frontend work.
