---
name: docs-drift
description: Check whether a code change leaves loco's user documentation stale (docs/content, README.md, examples/loco_example.toml, the behaviour sections of AGENTS.md) and fix the affected pages. Load after changing CLI commands or flags, API services, API or agent configuration, chart values, CRDs, loco.toml fields or mise tasks, and when a caller asks for a docs drift review of a diff or PR.
---

# Docs drift review

Compare a code change with the documentation that describes it, and bring the documentation
back in line. The scope is the change only: do not review the code, do not rewrite pages for
style, and do not touch text the change leaves accurate.

## Modes

Fix mode is the default. Edit the pages the change makes stale and nothing else.

When the caller says report-only, as the CI check in `.github/workflows/docs-drift.yml`
does, edit nothing. Apply the same judgment and report each confident stale or missing
update with the file and section that owns it.

## Required reading

1. The diff. Commit messages, the PR body and tests give context; the diff is the source of
   truth. Default to `git diff origin/main...HEAD` plus `git status` and `git diff` for
   uncommitted work. Use the branch, range, diff file or PR number the caller supplies instead.
2. `docs/zensical.toml`, whose `nav` lists every published page.
3. Every page under `docs/content/` that plausibly owns the changed behaviour, in full, plus
   `README.md` and `examples/loco_example.toml` when the change touches the CLI, loco.toml or
   local development. Headings and grep hits are not enough; the stale sentence is usually in
   the middle of a paragraph.
4. `.agents/skills/prose/SKILL.md` before writing or editing any prose.
5. `docs/content/contributing/documentation.md` for how the site is built and verified.

## Documentation in scope

The published site is the Zensical project in `docs/`; `docs/content/` holds its Markdown.
Pages state released behaviour and label unreleased workflows with a release notice.

- `docs/content/index.md`: overview, the "Where to start" table, release notices.
- `docs/content/getting-started/install.md`: `curl -fsSL https://loco.build/install.sh | sh`,
  `LOCO_VERSION`, `LOCO_INSTALL_DIR`, `--bin-dir`, `loco update`, `loco completion`.
- `docs/content/getting-started/connect.md`: `loco login`, `loco login --host`, `loco config set
  webHost`, the default hosts `https://api.loco.build` and `https://loco.build`, staging hosts.
- `docs/content/deployment/modes.md`: SaaS, dedicated and self-hosted modes and who operates
  what. Rarely affected by code.
- `docs/content/deployment/go-infrastructure.md`: the unreleased `loco infra` workflow, `loco
  init`, `loco validate`, `loco deploy --plan-only`, `loco secret set`. Carries a release notice;
  only a change to those commands or to the notice's facts affects it.
- `docs/content/deployment/environments.md`: hosted endpoint table, `loco infra context`,
  `LOCO_WORKSPACE` / `LOCO_ENVIRONMENT`, how UI and docs images are promoted.
- `docs/content/operations/applications.md`: `loco resource` subcommands, the `restricted` Pod
  Security requirement, public versus private applications (`[DomainConfig]`, `URL: none`,
  when domain changes take effect), rollout investigation.
- `docs/content/operations/self-hosting.md`: component table (directory per component),
  installation inputs via `.env.example`, cluster dependencies, image tags `sha-<commit>` and
  the chart values `agent.image.tag`, `controller.image.tag`, `builds.builderImage.tag`,
  `obsProxy.image.tag`, `mise run controller:gen`.
- `docs/content/reference/architecture.md`: control plane, regional runtime, observability
  components. Affected by adding, removing or renaming a component or module.
- `docs/content/reference/cli-api.md`: the CLI command table, the Buf Schema Registry link,
  `mise run gen`.
- `docs/content/reference/ai.md`: `llms.txt` exports. Affected only by `docs/zensical.toml`
  plugin changes.
- `docs/content/contributing/development.md`: `mise run setup`, `mise run tilt`, `.env.example`,
  the local ports `8000` and `5173`, `./bin/loco login --host`, the tasks it names.
- `docs/content/contributing/documentation.md`: docs tasks, build rules, theme files, hosting,
  `web/sws.toml`, `docs/scripts/package.py`.
- `README.md`: quick start (installer flags, `loco login`, `loco init`, `loco validate`, `loco
  deploy` and `--image`, source packing rules, `loco resource` and `loco builds` subcommands),
  the examples list, the contributing section (mise, Tilt, Dex on `localhost:5556`, the local
  registry and bucket, build flow, `charts/loco-operator` deployments and `builds.enabled`).
- `examples/loco_example.toml`: every loco.toml field with its default and whether it is
  required. loco.toml is being replaced by Go infrastructure definitions, so document the
  fields that exist today and do not expand this file beyond what `internal/config` validates.
- `AGENTS.md`, the "Domain Configuration" and "Regional Configuration" sections and the
  behaviour statements in "Dependencies and Configuration" (version reporting, `/version.json`,
  chart tags). The rest of `AGENTS.md` is process guidance, not product documentation.
- `docs/zensical.toml` only when a page is added, removed or renamed.

Out of scope: `docs/tdd/**`, `docs/notes.md`, `docs/dependencies.md`, `docs/DESIGN.md`,
`docs/checklist.txt`, `experiments/**`, `controller/README.md` and `web/README.md` (scaffold
boilerplate), `.agents/skills/**`, generated code (`gen/**`, `api/gen/**`,
`zz_generated.deepcopy.go`, CRD YAML under `controller/config` and `charts/loco-operator`),
tests, `e2e/**`, Go doc comments and `//` comments. A missing Go doc comment is not drift. The
repository has no changelog.

## Public code map

These paths produce behaviour a user, operator or contributor can observe. A change under them
is a candidate; judge what a reader can see, not the directory.

- `cmd/loco/**`: CLI commands, flags, printed output and exit behaviour. `root.go` registers the
  commands; each `Use:` and `Flags()` call is interface. Script tests in
  `cmd/loco/testdata/script/*.txtar` show the exact output a user sees.
- `web/public/install.sh`: the installer's flags and environment variables.
- `proto/**`: the API contract. A new or changed RPC, field or validation rule is visible on the
  Buf Schema Registry and through the CLI and dashboard that call it.
- `api/config.go`, `api/env.go`, `api/auth/config.go`, `.env.example`: the API's environment
  variables, defaults and validation. `.env.example` is the installation input document the
  self-hosting page points at; a new or renamed variable must appear there.
- `api/service/**`: observable API behaviour, error codes and messages, defaults the API fills
  in (`LOCO_DEFAULT_*`), limits such as `LOCO_SOURCE_MAX_BYTES`.
- `internal/config/**`: loco.toml fields, defaults and validation rules.
- `internal/sourcepack/**`: what `loco deploy` packs and excludes.
- `agent/config.go`, `builder/config.go`, `observability-proxy/**`: environment the charts
  must pass; a new required variable changes the chart contract.
- `charts/*/values.yaml` and `charts/*/templates/**`: install-time knobs (`builds.enabled`,
  image tags, `env.LOG_LEVEL`), namespaces and resource names an operator sees.
- `k8sapi/v1alpha1/*_types.go` and `k8sapi/v1alpha1/validator.go`: the Application and Build
  CRD schema and its validation.
- `controller/internal/**`: what the controller creates per Application (Service, HTTPRoute,
  NetworkPolicy, pull secret) and the Pod Security profile it enforces.
- `mise.toml`, `mise-tasks/**`, `Tiltfile`, `compose.yaml`, `env/local/**`: the documented
  development workflow, task names, local ports and credentials.
- `web/src/**`: dashboard behaviour that a page names (the new-service form's Networking
  choice, `/version.json`). Most UI changes have no documentation and are not drift.
- `web/Dockerfile`, `web/sws.toml`, `docs/scripts/**`, `docs/zensical.toml`: how the docs are
  built and served, documented in `contributing/documentation.md`.

## What counts as drift

Strong candidates:

- A command, subcommand, flag, environment variable, loco.toml field, chart value, CRD field,
  mise task or API RPC that a page names is added, renamed, removed or changes meaning.
- A default, limit, port, hostname, path, exclusion rule or error message that a page states
  changes.
- Printed CLI output that a page quotes changes (`URL: none`, the "no public URL" line).
- A requirement changes: image user, ports, required environment, cluster dependencies.
- A component, module or chart is added, removed or renamed, so a table or component list is
  wrong.
- A page is added, removed or renamed without a matching `docs/zensical.toml` change.
- An unreleased workflow ships or an unreleased command changes, so a release notice is wrong.

Not drift:

- Refactors, test-only, e2e-only, CI-only and dependency changes with no observable effect.
- Bug fixes that restore the behaviour a page already describes.
- Changes to behaviour no page mentions, when no page's current statements become wrong and the
  behaviour has no owning section. Record it as a candidate left alone; do not invent a section.
- Changes already documented in the same diff. When the diff edits the owning page, check that
  the edit is complete and accurate, then report resolved.
- Drift that predates the diff. Mention it separately as pre-existing and leave it for its own
  change.

Do not require both `README.md` and a `docs/content/` page unless each states the stale fact.
When one page links to another for the detail, update the page that owns the detail.

## Routing

- CLI commands and flags: `reference/cli-api.md` for the command table, `operations/
  applications.md` for `loco resource`, `getting-started/connect.md` for `loco login` and
  `loco config`, `getting-started/install.md` for the installer, `loco update` and
  `loco completion`, `README.md` quick start for `loco deploy`, `loco builds` and packing rules.
- Domain and routing behaviour: `operations/applications.md` and the Domain Configuration
  section of `AGENTS.md`.
- loco.toml fields: `examples/loco_example.toml`, then the Regional or Domain Configuration
  section of `AGENTS.md` when it enumerates the rule.
- API and agent environment variables: `.env.example`; `operations/self-hosting.md` only when
  its prose names the variable or the category changes (database, cache, auth, registry,
  endpoints).
- Chart values, image tags, namespaces, `builds.enabled`: `operations/self-hosting.md`, the
  README contributing section when it states the value.
- Components and modules: the table in `operations/self-hosting.md`, `reference/
  architecture.md`, the Architecture section of `AGENTS.md`.
- Local development (tasks, ports, Dex, registry, bucket): `contributing/development.md` and
  the README contributing section.
- Docs build and hosting: `contributing/documentation.md`.
- API schema: `reference/cli-api.md` names the registry and `mise run gen` only; field-level
  changes need no page unless the CLI or dashboard behaviour they drive is documented.

## Workflow

1. Determine the diff and read it whole.
2. List each observable change with the surface it affects (CLI, dashboard, API, loco.toml,
   environment, chart, CRD, development workflow).
3. For each change, read the owning pages in full and find the sentence, table row, code
   block or notice that states the old behaviour, or confirm none does.
4. Decide drift, already covered, pre-existing or not documented.
5. In fix mode, make the smallest edit that makes the page true. Match the surrounding heading
   level, sentence style, admonition and code block style. Describe current behaviour, never
   the change. Keep release notices on unreleased workflows.
6. In fix mode, verify: `mise run docs:build` (strict, fails on warnings) and `mise run
   test:docs` when a `docs/` file changed. Report a verification you could not run instead of
   skipping it silently.
7. When it is unclear whether a change is observable or which page owns it, say so. In
   report-only mode, report drift only when you can name the change and the exact stale or
   missing location.

## Output

Report-only mode follows the caller's required format. Each finding is one bullet naming the
file, the section and the stale or missing statement, with the code change that causes it.
No code review findings, no style notes, no speculative additions.

Fix mode reports, in order:

1. Pages and sections edited, each with the code change that required it.
2. Observable changes left alone, with the reason (already covered, not documented anywhere,
   internal).
3. Pre-existing drift noticed, ambiguous items, and verification that could not run.

When nothing needs an update, say so and give the reason in one sentence.
