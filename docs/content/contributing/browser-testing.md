---
title: "Dashboard browser tests"
---

The dashboard uses Python Playwright with headless Chromium to check interactions that cross React state, navigation, permissions and API requests. Bun tests cover pure models and helpers; API database tests cover authorization and persistence. Browser tests run the production bundle against an isolated fixture.

## Scope

Browser tests use the [UI evidence fixture](https://github.com/team-loco/loco/blob/main/tests/ui/evidence.py). It builds a git ref, starts its API with its own database on a PostgreSQL server every run on the machine shares, seeds test identities, and serves the production dashboard on free loopback ports. No Kubernetes cluster, shared development database, identity provider or personal browser session is required.

Add a browser regression when unit tests cannot show the interaction failing: cursor recovery, keyboard behavior, scoped navigation or responsive controls. Keep calculations in Bun tests and backend policy in real database tests. Controlled response fixtures, such as the trace viewer's telemetry fixture, must be identified as fixtures rather than evidence that the backend works.

## Run locally

Install the pinned tools and start Docker or OrbStack:

```sh
mise run setup
uv run --frozen --project tests playwright install chromium
mise run test:ui deployment_history_ui
```

`mise run test:ui` with no arguments runs every suite in `tests/ui/`: each `*_ui.py` file drives Chromium and each `*_api.py` file calls the API alone. A new suite is one new file there; add its CI duration in seconds to `tests/ui/durations`. Suites run side by side, one per two CPUs or `--jobs N` at a time, longest first, and each suite's output prints as one block when it ends. A suite named several times runs that many times, labelled `name`, `name#2` and so on, which repeats a suite to look for a flake. `--shard i/N` runs the ith of N shards with equal expected duration. The task builds the API and production UI once. A clean checkout reuses the build of its commit from a machine-wide cache, so every checkout and agent builds a commit once; uncommitted changes build in place and never enter the cache. Each suite then starts its own API and UI processes and creates its own database on the PostgreSQL server and seeded OpenBao that every run on the machine shares; the server stops when no run uses it. The suite seeds its database, drives the page in its own browser context of the headless Chromium every run on the machine shares, and removes its resources on success or failure. The shared Chromium starts on demand through `playwright launch-server` and stops when no run uses it. The UI reads its API address from `/runtime-config.js`, as it does behind the chart, so one build serves every suite. Running a suite file directly uses the cached build of `HEAD`, building it in a temporary worktree when missing. Users and hashed session tokens are inserted only in that database because creating identities requires an external provider. Organizations, workspace membership, environments and resources use the API. Historical deployment and placement rows are inserted privately to represent 221 releases and three controller reports without running builds or clusters. Each environment uses separate fixture clusters.

## Run in CI

`mise run ci:evidence` runs named suites in CI for the pushed head of the current branch, waits for that run (joining one already going for the same commit and inputs) and downloads its output into `--out`. It exits non-zero when a suite fails.

```sh
mise run ci:evidence --out /tmp/loco-suites tables_ui deployment_history_ui
```

The download holds `suites.log`, the combined suite output, and `suites/`, each suite's output directory. A draft pull request runs no CI, so this is how a draft gets its browser suites run. The [CI evidence skill](https://github.com/team-loco/loco/blob/main/.agents/skills/ci-evidence/SKILL.md) describes the workflow for agents.

To inspect a failed fixture locally, run the driver with `--keep`; it prints its state path. Reuse that owned fixture with `--state PATH`. This runs against the production build recorded in the state, not newly edited source. Remove it afterward with `uv run --script tests/ui/evidence.py down --state PATH`. Do not reuse a person's development stack or publish its authentication state.

## Deployment history

`tests/ui/deployment_history_ui.py` checks these cases once in light mode:

- The first API page contains 200 of 221 production releases. A long-lived current deployment in Europe is outside that page and appears through its independent `GetDeployment` request.
- The table reaches older history through the API's actual opaque cursor. The displayed count changes from loaded to total when the cursor is exhausted.
- Exactly one older-page request receives a deliberate browser-injected 503. The displayed rows and current regional deployment survive. Explicit keyboard retry uses the same request/cursor and retrieves the remaining 21 records. All successful history and current-deployment responses come from the API.
- Switching the breadcrumb environment changes the requested environment and current deployment, resets the local pager, and removes production history from the staging table.
- Workspace settings enable Name for the owner and disable it for a workspace reader. The reader has a real scoped session, not a mocked permission response.

The injected 503 checks frontend recovery, not backend failure handling. The SQL history fixture checks browser/API integration, not controller reconciliation or application health. The fixture has no observability service; unrelated metric panels can show their unavailable state.

## CI, failures and ownership

The Web workflow installs pinned dependencies and Chromium, builds and checks the dashboard, and four browser jobs each run a quarter of the expected suite time with `mise run test:ui --shard i/4`, which continues past a failing suite and reports every failure at the end. The suite step in each browser job has a 30-minute bound. The suite records fixture-startup and total seconds in `tests/ui/out/deployment-history/results.json`; use those measurements and CI step duration when assessing cost. There are no automatic assertion or job retries. Playwright's bounded locator waits handle asynchronous rendering. A deliberate retry button click belongs to the tested behavior.

When a suite fails, `harness.session` in `tests/ui/harness.py` saves, for every page still open, a screenshot (`failure-<context>-<page>.png`), its URL, JavaScript errors and failed requests (`failure-<context>-<page>.json`), and a Playwright trace of each browser context (`failure-<context>.zip`) into the suite's output directory. [Trace Viewer](https://playwright.dev/python/docs/trace-viewer-intro) shows the failed actions and network requests. Traces can include authentication and response data: retain only disposable synthetic fixtures in CI artifacts; never upload shared development recordings. The workflow uploads artifacts even when the step fails. Behavioral checks run once in light mode; viewport changes exercise layout-specific assertions. `tests/ui/harness.py` loads isolated fixtures, opens authenticated pages and calls the API. Original exceptions and exit status remain failures; cleanup does not turn them into passes.

The author changing a flow owns its assertions and fixture updates. Reviewers check that fixture changes preserve the regression and represent the current contract. The failing change's owner first distinguishes an assertion regression, fixture/schema drift, production-build error or runner failure using the saved artifacts. Fix the cause before rerunning; a single diagnosed infrastructure rerun is evidence, while repeated runs until green are not a resolution. Do not update screenshot baselines to hide a behavior change.

## PR evidence

For UX/UI-only work, start `mise run ui:staging` and capture the local dashboard against real staging data. This uses the saved staging CLI login and starts no local API, PostgreSQL or OpenBao. See [UI changes against staging](development.md#ui-changes-against-staging) for setup. Use existing data for screenshots; actions in the dashboard change staging.

Capture before and after in light and dark mode and include a narrow viewport for layout changes. Label the screenshots as real staging data; live values may differ between captures. Inspect every image before attaching it to the pull request. Keep authentication state and staging browser traces out of commits and public attachments. Use the CI evidence fixture when local browser access or suitable staging data is unavailable. Browser regression suites remain isolated and deterministic; do not point them at staging.

Browser assertions check behavior continuously. The [CI evidence skill](https://github.com/team-loco/loco/blob/main/.agents/skills/ci-evidence/SKILL.md) supplies reviewed before/after screenshots attached directly to UI PR descriptions; `mise run ci:evidence --shots FILE` takes them in CI, and the [UI evidence skill](https://github.com/team-loco/loco/blob/main/.agents/skills/ui-evidence/SKILL.md) takes them locally. Cover light/dark and narrow layouts, state the build refs and whether API data is real or replayed, and inspect every image before upload. CI screenshots diagnose that run; they are not automatically a meaningful before/after comparison. A test-only change can attach its test states without claiming a product redesign.
