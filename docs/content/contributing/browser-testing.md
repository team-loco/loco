# Dashboard browser tests

The dashboard uses Python Playwright with headless Chromium to check interactions that cross React state, navigation, permissions and API requests. Bun tests cover pure models and helpers; API database tests cover authorization and persistence. Browser tests run the production bundle against an isolated fixture.

## Decision and scope

Use the existing Playwright dependency and [UI evidence fixture](https://github.com/team-loco/loco/tree/main/.agents/skills/ui-evidence). It builds a git ref, starts its API and PostgreSQL, seeds test identities, and serves the production dashboard on free loopback ports. No Kubernetes cluster, shared development database, identity provider or personal browser session is required.

[Meticulous records user interactions and network responses, selects sessions and replays them against base/head builds](https://app.meticulous.ai/docs/concepts/architecture-overview). That offers broad visual coverage, with a recorder, service integration, recording/data review and visual-diff triage to maintain. It replays recorded responses; backend authorization still needs separate tests. Playwright keeps fixtures and explicit assertions in this repository and already runs in Web CI. Its costs are test maintenance, isolated API/PG startup, browser installation and CI compute. The pilot adds no hosted testing subscription.

Start with deployment history, environment switching and permission-dependent controls. Add a browser regression when unit tests cannot show the interaction failing: cursor recovery, keyboard behavior, scoped navigation or responsive controls. Keep calculations in Bun tests and backend policy in real database tests. Controlled response fixtures, such as the trace viewer's telemetry fixture, must be identified as fixtures rather than evidence that the backend works.

## Run locally

Install the pinned tools and start Docker or OrbStack:

```sh
mise run setup
uv run --frozen --project docs playwright install chromium
mise run e2e:deployment-history
```

The task builds `HEAD`, creates its own API/UI processes and disposable PostgreSQL container, runs Chromium and removes its resources on success or failure. Users and hashed session tokens are inserted only in that database because creating identities requires an external provider. Organizations, workspace membership, environments and resources use the API. Historical deployment and placement rows are inserted privately to represent 221 releases and three controller reports without running builds or clusters. Each environment uses separate fixture clusters.

To inspect a failed fixture locally, run the driver with `--keep`; it prints its state path. Reuse that owned fixture with `--state PATH`. This runs against the production build recorded in the state, not newly edited source. Remove it afterward with `uv run --script .agents/skills/ui-evidence/scripts/evidence.py down --state PATH`. Do not reuse a person's development stack or publish its authentication state.

## Deployment-history pilot

`tests/e2e/deployment_history_ui.py` checks these cases in light/dark mode at desktop and 390px:

- The first API page contains 200 of 221 production releases. A long-lived current deployment in Europe is outside that page and appears through its independent `GetDeployment` request.
- The table reaches older history through the API's actual opaque cursor. The displayed count changes from loaded to total when the cursor is exhausted.
- Exactly one older-page request receives a deliberate browser-injected 503. The displayed rows and current regional deployment survive. Explicit keyboard retry uses the same request/cursor and retrieves the remaining 21 records. All successful history and current-deployment responses come from the API.
- Switching the breadcrumb environment changes the requested environment and current deployment, resets the local pager, and removes production history from the staging table.
- Workspace settings enable Name for the owner and disable it for a workspace reader. The reader has a real scoped session, not a mocked permission response.

The injected 503 checks frontend recovery, not backend failure handling. The SQL history fixture checks browser/API integration, not controller reconciliation or application health. The fixture has no observability service; unrelated metric panels can show their unavailable state.

## CI, failures and ownership

The Web workflow installs pinned dependencies and Chromium, builds and checks the dashboard, then runs the focused browser suites. The deployment-history step has a 15-minute bound. The suite records fixture-startup and total seconds in `tests/e2e/out/deployment-history/results.json`; use those measurements and CI step duration when assessing cost. A macOS/OrbStack run on 2026-10-16 took 34.49 seconds, including 10.23 seconds to build and start the isolated fixture, with tool downloads and build caches already present. These are local measurements, not a cold CI budget. There are no automatic assertion or job retries. Playwright's bounded locator waits handle asynchronous rendering. A deliberate retry button click belongs to the tested behavior.

Failure artifacts include the current PNG, case/error JSON, JavaScript errors, scoped deployment request bodies and a Playwright trace. [Trace Viewer](https://playwright.dev/python/docs/trace-viewer-intro) shows the failed actions and DOM. Traces can include authentication and response data: retain only disposable synthetic fixtures in CI artifacts; never upload shared development recordings. The workflow uploads artifacts even when the step fails. Original exceptions and exit status remain failures; cleanup does not turn them into passes.

The author changing a flow owns its assertions and fixture updates. Reviewers check that fixture changes preserve the regression and represent the current contract. The failing change's owner first distinguishes an assertion regression, fixture/schema drift, production-build error or runner failure using the saved artifacts. Fix the cause before rerunning; a single diagnosed infrastructure rerun is evidence, while repeated runs until green are not a resolution. Do not update screenshot baselines to hide a behavior change.

## PR evidence

Browser assertions check behavior continuously. The [UI evidence skill](https://github.com/team-loco/loco/blob/main/.agents/skills/ui-evidence/SKILL.md) supplies reviewed before/after screenshots attached directly to UI PR descriptions. Cover light/dark and narrow layouts, state the build refs and whether API data is real or replayed, and inspect every image before upload. CI screenshots diagnose that run; they are not automatically a meaningful before/after comparison. A test-only change can attach its pilot states without claiming a product redesign.
