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

- **Imports**: stdlib → third-party → local packages; grouped with blank lines
- **Error handling**: Wrap with `fmt.Errorf("context: %w", err)`; use `slog.ErrorContext()`
- **Errors stay visible**: log a failure that needs a person at ERROR, and stop rather than retry an error a retry cannot fix (a removed resource, a rejected credential, invalid configuration). WARN is only for a condition the code handles itself, such as a dropped stream it reconnects.
- **Naming**: camelCase for unexported, PascalCase for exported; descriptive names
- **Types**: Use protobuf types for API; shared types in `internal/`, CLI-only packages in `cmd/internal/`. Start with concrete types: accept interfaces, return concrete types, and add an interface only when a second real implementation exists, never just so a test can fake one
- **Logging**: `slog` package with structured logging; `slog.InfoContext(ctx, "message")`
- **Concurrency**: Context-based cancellation; goroutines with proper cleanup
- **Formatting**: `gofmt` compatible; single import per line in groups.
- **Comments** explain why, never what: a constraint, a non-obvious decision, the bug a line
  avoids. Delete comments that restate the code. Follow-ups and deprecations go in an issue,
  not a comment. Comments are lower-case unless they are Go doc comments.
- **Switch statements**: never group `case` labels to share a body. Every case gets its own body, even when two cases return the same value — grouped labels are a fallthrough, and a later edit that adds a statement under the first label silently changes the second. Prefer an explicit `case X: return "a"; case Y: return "a";` over `case X: case Y: return "a";`.
- **Proto Optional Fields**: Always use getter syntax (e.g., `r.GetDescription()`) instead of pointer checks. Never write `r.Description != nil && *r.Description != ""`. Set one with `new`: `Name: new("renamed")`.
- **Errors**: don't scatter `errors.New()` / `fmt.Errorf()` through function bodies. Any error with a fixed message is a package-level variable: in the package's `errors.go` when several files use it, otherwise in a `var` block at the top of the file. Reuse an existing sentinel before declaring a new one. Inline is only for errors that wrap or format runtime values (`fmt.Errorf("context: %w", err)`).
- **Magic numbers**: no unexplained literals in logic (`5 << 20`, `1024`, `65535`, timeouts, limits, ports). Tunable values belong in the config struct; the rest are named `const`s at the top of the file. Before submitting, check the whole change for any you missed.
- **Names**: never use numbered suffixes (`uniqueJSONErr2`, `uniqueJSONErr3`). Name each value after what it holds.
- **New tooling is Go**: write a new tool or helper script as a Go program under `tools/`, tested
  with `go test`, not in Python. Existing Python tooling stays until it is rewritten for its own
  reasons.
- **Standard library first**: before hand-rolling parsing, matching or a lookup table, check
  whether the standard library or an existing dependency already does it, e.g.
  `netip.Addr.IsLoopback` instead of a list of loopback hosts, `net/url` instead of string
  splitting.
- **Nested calls**: name a call's result in a local before passing it to another call or a struct literal.

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

- **PRs, issues and commits are read by strangers.** Never write anything conversational
  into a PR description, issue body, commit message or code review comment: no offers
  ("happy to do that as a follow-up"), no questions to the reader, no narration of what you
  did or did not try, no apologies. Those belong in the chat reply to whoever you are working
  for. A PR body states what changed, why, and anything a reviewer must know to judge it --
  nothing else. Follow-up work that is worth tracking becomes an issue, not a sentence in a
  description. Do not add AI attribution either: no `Co-Authored-By` trailer for an agent, no
  "Generated with ..." footer, no session links. Load the `prose` skill before writing any of
  these.
- **Frontend PRs carry before and after screenshots.** Any PR that changes what the web UI
  looks like or how it behaves must include screenshots of the affected screens before and
  after the change in the PR body, as evidence. Cover light and dark mode, and a narrow
  viewport when the layout changes. The `ui-evidence` skill captures and tabulates them.
  Upload them with `gh pr edit --attach`.
- **Other agents may be working in this repo at the same time.** The index is shared
  state: never run `git commit` on whatever happens to be staged. Check `git status`
  first, and commit explicit paths (`git commit -- <paths>`) so another agent's staged
  work cannot be swept into your commit.
- **PRs must be up to date with `main` to merge.** Branch protection requires it, and
  `CI Gate` reruns on the updated head. Rebase onto `origin/main` before asking for a merge.
  Commits that landed on `main` meanwhile can change what your work depends on: read
  `git log HEAD..origin/main` and the files those commits touch, and fix renamed symbols,
  moved files, new rules in this file and conflicting migrations or protos before pushing,
  not only the textual conflicts git reports.
- **Use `gh stack` for readable PRs.** Whenever a change can be split into a chain of
  small, reviewable PRs that build on each other, create them with `gh stack` instead of
  one large PR or hand-rolled branches off each other.
- **Check PR state before pushing.** A draft can be merged or closed while you work on it.
  Run `gh pr view` before pushing more commits to its branch.
- **Buf compares against `main`**, so every PR in a stack above a breaking proto change also
  needs the `break-buf` label.
- **This repo is public.** Never put exploitable weaknesses — missing auth, forgeable attributes, over-broad credentials,... Never also run a plain `git add .` or `git add -A`, without knowing it is safe. Assume all git issues, notes, comments, code, is visible to everyone in the world.

## Generated Code

Never hand-edit. Regenerate via `mise run gen` after any changes to `proto/`, `api/queries/` or
  `api/migrations/`.

- `k8sapi/**/zz_generated.deepcopy.go`, `controller/config/crd/bases/**`,
  `controller/config/rbac/role.yaml` (Application controller, from the markers in
  `controller/internal/controller`), `controller/config/rbac/builds/role.yaml` (build
  controller, from `controller/internal/builds`) and the `charts/loco-worker` copies of
  the roles: `templates/controller/manager-role.yaml` and
  `templates/builds/manager-role.yaml` — `mise run controller:gen`. CI fails when any of
  them is out of date. Read the diff: unrelated-looking changes in the chart copies are
  usually real drift.
- `charts/*/values.schema.json`, from the `# @schema` annotations in each chart's
  `values.yaml` — `mise run chart:schema`. `mise run lint:charts` fails when a schema is out
  of date.
- No chart installs CRDs. `mise run crds:apply` server-side applies
  `controller/config/crd/bases/*.yaml` and the Gateway API CRDs from the pinned
  `gateway-helm` chart before any chart is installed.
- The database has no users yet: edit migrations in place instead of adding new ones.

## Linting

golangci-lint is pinned in `mise.toml` at the version CI runs, and the lefthook pre-commit
hook runs it on the Go modules a commit touches. **CI gates on it**, so write lint-clean the
first time and run `mise run lint:go` before pushing. The one that bites most:

- `errcheck` flags unchecked type assertions. Always `v, ok := x.(T)` with a handled
  `!ok`, never `v := x.(T)`.

## Charts

- **A chart change comes with a helm-unittest test** in the chart's `tests/` that fails before
  the change and passes after it. Assert the property the change is about; never use
  `matchSnapshot`. `charts/TESTING.md` covers the static tests (`mise run lint:charts`), the
  unit tests (`mise run test:charts`) and the collector configuration tests that stay pytest.
- **Bump the chart's version** when a file the chart packages changes; `lint:charts` fails
  otherwise. `tests/` and `ci/` are not packaged.

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

- **Components**: `web/src/components/ui/` is stock vendored shadcn and is never edited. All Loco
  styling goes in `components/design/<Name>.tsx` wrappers layered on with `cn()`, and pages import
  from `design/`.
- **Colours**: use the theme tokens (`bg-bg2`, `text-fg3`, `border-line`, the `ok/warn/bad`
  pairs), never raw hex or stock Tailwind palette colours. Check every change in light and dark.
- **Cursors**: the base CSS sets the pointer and disabled cursors; do not add `cursor-pointer`
  per element.
- **Effects**: no `useEffect` unless it syncs with an external system. Derive values during
  render and do work in event handlers; for timers or observers use `useSyncExternalStore` or a
  ref callback that returns a cleanup. Follow
  https://github.com/vercel-labs/agent-skills/blob/main/skills/react-best-practices/AGENTS.md.
- **Production build**: the dev server does not catch bundling bugs. Any change to
  `web/build.ts` or dependencies needs `bun run build` and `bun run preview`, loaded in a browser,
  before the PR.
- **Errors**: user-facing error text goes through `web/src/lib/error-handler.ts`:
  `getErrorMessage(error, fallback)` for a string, `toastConnectError(error, fallback)` for a
  toast. Both handle `ConnectError`, `Error` and unknown values.

## Skills

Task-specific guidance lives in `.agents/skills/` (`.claude/skills` links to it), and the
subagents those skills run in live in `.agents/agents/` (`.claude/agents` links to it).
Claude Code loads a skill when its description matches the task; other agents should read
the matching `SKILL.md` before starting.
