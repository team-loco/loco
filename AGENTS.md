# Loco Agent Guidelines

## Goal

- Loco is a container orchestration platform that simplifies application deployment. Run `loco deploy` and Loco handles the rest - building, deploying, and scaling your applications on Kubernetes. Competitor to heroku/railway.

## Architecture

- **CLI**: Go app using Cobra + Charm libraries (Bubble Tea, Lipgloss) for terminal UI
- **API Server**: Go with ConnectRPC (gRPC over HTTP), PostgreSQL database
- **Infrastructure**: Kubernetes with Cilium networking, Envoy Gateway, cert-manager, OpenTelemetry, ClickHouse metrics, Grafana
- **Key modules**: `api/` (ConnectRPC services), `cmd/loco/` (CLI commands), `agent/` (in-cluster agent), `controller/` (kubebuilder operator), `k8sapi/` (CRD types), `internal/` (shared logic), `web/` (dashboard), `gen/` (generated protobuf types)

## Build/Lint/Test Commands

Builds, tests, code generation, linting and the local environment are mise tasks: `mise tasks` lists them, `mise run <task>` runs one. Every tool (go, bun, golangci-lint, buf, sqlc, helm, kind, ...) is pinned in `mise.toml` and `mise.lock`, and CI installs the same versions; `mise run setup` installs them, the web dependencies and the git hooks. `controller/Makefile` is the kubebuilder scaffold and stays; the root tasks call into it. Add a task to `mise.toml` (or a script under `mise-tasks/`) for commands you find yourself running often.

## Principles

- **Every change has a test** that fails before it and passes after it.
- **Own regressions.** A test that fails after your change is yours to diagnose; don't argue it was already broken without proof.
- **Evidence over assumptions.** Reproduce a bug before fixing it, and find the root cause instead of patching the symptom.
- **Fail fast on invalid states.** Assert invariants; don't hide defects behind defensive conditionals.
- **Clean as you go.** Fix stale code you find in the area you touch (unread fields, outdated
  comments, unused files) in the same PR.
- **Features ship with an e2e suite**: one `mise run e2e:<suite>` that builds its own kind
  cluster from a clean machine and cleans up after itself. Extend `e2e/`.
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
  images.
- **A build reports the version it was tagged as.** Every Go binary reads it from
  `-ldflags "-X main.version=..."`, which each Dockerfile fills from the `VERSION` build arg.
  `build-push.yml` passes the image tag (`sha-<commit>`), the CLI release its git tag
  (`vX.Y.Z`), and e2e `e2e`, the tag of its images. Without one, a binary reports its Go
  module version, `(devel)` inside Docker. The charts have no default tag for Loco images.
- **One source per value.** Don't repeat a chart value as a Go default or a script constant.
- **Read configuration once at startup** into a config struct, like `newAPIConfig()` in
  `api/main.go`; no `os.Getenv` elsewhere. Invalid configuration panics. Tunable limits,
  timeouts and defaults are config fields, not literals in the code.

## Code Style

- **Imports**: stdlib → third-party → local packages; grouped with blank lines
- **Error handling**: Wrap with `fmt.Errorf("context: %w", err)`; use `slog.ErrorContext()`
- **Naming**: camelCase for unexported, PascalCase for exported; descriptive names
- **Types**: Use protobuf types for API; shared CLI types in `internal/`; interfaces for abstraction
- **Logging**: `slog` package with structured logging; `slog.InfoContext(ctx, "message")`
- **Concurrency**: Context-based cancellation; goroutines with proper cleanup
- **Formatting**: `gofmt` compatible; single import per line in groups.
- **Comments**: never add a comment unless it is explicitly requested. this applies to
  every comment, including ones explaining a non-obvious fix, justifying a change, or
  pointing at a deprecation or a follow-up -- put that in the commit message or the PR
  body instead, where it does not rot. `//nolint` and other tool directives are not
  comments for this purpose; keep them minimal. when a comment is requested: it must not
  be capitalized, unless it is go style (for function / struct / interface docstring).
- **Switch statements**: never group `case` labels to share a body. Every case gets its own body, even when two cases return the same value — grouped labels are a fallthrough, and a later edit that adds a statement under the first label silently changes the second. Prefer an explicit `case X: return "a"; case Y: return "a";` over `case X: case Y: return "a";`.
- **Proto Optional Fields**: Always use getter syntax (e.g., `r.GetDescription()`) instead of pointer checks. Never write `r.Description != nil && *r.Description != ""`.
- **Errors**: don't scatter `errors.New()` / `fmt.Errorf()` through function bodies. Any error with a fixed message is a package-level variable: in the package's `errors.go` when several files use it, otherwise in a `var` block at the top of the file. Reuse an existing sentinel before declaring a new one. Inline is only for errors that wrap or format runtime values (`fmt.Errorf("context: %w", err)`).
- **Magic numbers**: no unexplained literals in logic (`5 << 20`, `1024`, `65535`, timeouts, limits, ports). Tunable values belong in the config struct; the rest are named `const`s at the top of the file. Before submitting, check the whole change for any you missed.
- **Names**: never use numbered suffixes (`uniqueJSONErr2`, `uniqueJSONErr3`). Name each value after what it holds.
- **Nested calls**: name a call's result in a local before passing it to another call or a struct literal.

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
  viewport when the layout changes. Upload them through the PR editor as attachments; never
  push image files or image branches.
- **Other agents may be working in this repo at the same time.** The index is shared
  state: never run `git commit` on whatever happens to be staged. Check `git status`
  first, and commit explicit paths (`git commit -- <paths>`) so another agent's staged
  work cannot be swept into your commit.
- **Use git worktrees.** Do branch work in its own worktree (`git worktree add <path> -b
  <branch> origin/main`, under a scratch directory), not by switching branches in the main
  checkout. The main checkout stays on `main` for everyone else. Remove the worktree once
  its PR merges.
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
  controller, from `controller/internal/builds`) and the `charts/loco-operator` copies of
  them: `templates/crd/*.infra.loco.io.yaml`, `templates/controller/manager-role.yaml` and
  `templates/builds/manager-role.yaml` — `mise run controller:gen`. CI fails when any of
  them is out of date. Read the diff: unrelated-looking changes in the chart copies are
  usually real drift.
- The database has no users yet: edit migrations in place instead of adding new ones.

## Linting

golangci-lint is pinned in `mise.toml` at the version CI runs, and the lefthook pre-commit
hook runs it on the Go modules a commit touches. **CI gates on it**, so write lint-clean the
first time and run `mise run lint:go` before pushing. The one that bites most:

- `errcheck` flags unchecked type assertions. Always `v, ok := x.(T)` with a handled
  `!ok`, never `v := x.(T)`.

## Domain Configuration

- **Location**: `internal/config/types.go` (types), `internal/config/loader.go` (validation,
  `ExtractSubdomainFromHostname()`), `cmd/loco/resource/resolve.go` (`resolveDomainInput`)
- **Types**: `DomainConfig` has Type (platform/custom) and Hostname (full resolvable hostname);
  the subdomain is the leftmost label of Hostname
- **In loco.toml**:
  - `[DomainConfig]`: optional; without it the app gets no internet traffic
  - `Type`: "platform" (default, Loco-provided) or "custom" (validates, but deploy rejects it)
  - `Hostname`: full resolvable hostname (e.g., "myapp.onloco.app")
- **Deploy flow**: `resolveDomainInput` matches Hostname against the active platform domains by
  suffix, and falls back to an interactive pick when none match

## Regional Configuration

- **Location**: `internal/config/types.go` (`Resources`), `internal/config/loader.go`
  (validation), `cmd/loco/resource/deploy_image.go` and `deploy_deployment.go` (deploy)
- **Structure**: `RegionConfig` maps region names to `Resources`; each region is explicit, with
  no global defaults
- **In loco.toml** (flat keys, as `loco init` writes them):
  - `[RegionConfig."region-name"]`: `CPU`, `Memory`, `ReplicasMin`, `ReplicasMax` are required
  - `ReplicasMin` > 0, `ReplicasMax` between `ReplicasMin` and 3
  - `EnableAutoScaling`, with exactly one of `CPUTarget` or `MemoryTarget` (1-100) when enabled
  - `Metadata.Region` names the region the deployment targets; at least one region is required
- **Reference**: `examples/loco_example.toml` documents every field

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
  `vite.config.ts` or dependencies needs `bun run build` and `vite preview`, loaded in a browser,
  before the PR.
- **Errors**: user-facing error text goes through `web/src/lib/error-handler.ts`:
  `getErrorMessage(error, fallback)` for a string, `toastConnectError(error, fallback)` for a
  toast. Both handle `ConnectError`, `Error` and unknown values.

## Skills

Task-specific guidance lives in `.agents/skills/` (`.claude/skills` links to it). Claude Code
loads a skill when its description matches the task; other agents should read the matching
`SKILL.md` before starting.
