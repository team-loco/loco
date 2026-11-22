---
name: ui-evidence
description: Capture before and after screenshots of the web UI, in light and dark mode, for a PR that changes what the web UI looks like or how it behaves. Captures locally and clicks through a running app, with each commit built once into a machine-wide cache, run with its own database on a Postgres server and OpenBao every run shares, and shot in its own context of a shared headless Chromium; the `ci-evidence` skill takes them in CI. Load before taking PR screenshots.
context: fork
agent: ui-evidence
allowed-tools: Bash, Read, Write
---

# UI evidence

`scripts/evidence.py` does every brittle step. Run it with uv from the repository root; it never
touches the dev stack (`mise run dev`), Dex or a person's browser windows.

## Capture locally

`up` leaves each ref's API and UI running until `down`.

```sh
E="${CLAUDE_SKILL_DIR}/scripts/evidence.py"
mise exec -- uv run --script "$E" <command>
```

1. Put inputs and screenshots outside the checkout. `up --ref HEAD` rejects untracked
   files as well as tracked edits.

   ```sh
   capture=$(mktemp -d "${TMPDIR:-/tmp}/loco-capture.XXXXXX")
   cp .agents/skills/ui-evidence/scenario.example.json "$capture/scenario.json"
   cp .agents/skills/ui-evidence/shots.example.json "$capture/shots.json"
   ```

   Edit the scenario JSON: the users, the organizations each owner creates with their
   workspaces and members, and workspace grants. Start from `scenario.example.json`.
   An optional `resources` list seeds synthetic services. Each entry names a `workspace`
   (`org/workspace`), a `name` and a `deployments` list; an entry with a `service` spec
   creates the service from that spec and may omit `deployments`. Each deployment specifies
   an existing `environment`, `region`, deployment `status`, placement `ready_replicas`
   and a service deployment `spec` with `observability`, an image `build`, `cpu`, `memory`, `minReplicas`,
   `maxReplicas` and optional `port`. Use one deployment per environment and region.
   Start from `.agents/skills/ui-evidence/scenario.resources.example.json`. To seed what the cluster
   agent reports, a region under `placements` may also list `replicas` (the agent's replica
   reports: `name`, `ready`, `state`, `restarts`, `started_at`, `last_exit`, `last_restart`; `uid`,
   `deployment_id` and `placement_revision` default to the name, the deployment and 1) and a
   rollout `problem` (`reason`, `message`, `detail`, `replica_name`), and a resource may list
   platform `domains` (`{"environment": "production", "domain": "web.apps.example.test"}`), which
   make it public. Give a deployment an `id` when another deployment's replica names it. A deployment's
   `build` seeds the build it came from, as `builds` columns such as `status`, `image_digest`,
   `git_commit`, `created_at`, `started_at` and `finished_at`, and adds its ID to the spec; a
   shot's `respond` for `ObservabilityService/QueryLogs` supplies the build's log lines. Time
   placeholders such as `{"seconds": "{now-3h:s}"}` work in these fields. Start from
   `.agents/skills/ui-evidence/scenario.agent.example.json`. Resources use the API; synthetic
   clusters, deployments and placements use SQL without contacting a registry or worker.
   The state file's `resources` maps `org/workspace/name` to resource IDs for shot paths,
   `orgs` maps each organization name to its `id` and `owner`, and `environments` maps
   `org/workspace/slug` to environment IDs. A placement's `status` (for example `"failed"`)
   overrides the deployment status for that region. A top-level `events` list seeds workspace
   events: each has a `type` (such as `resource.degraded` or `deployment.ready`), a `resource`
   (`org/workspace/name`) or a `workspace`, an optional `environment` slug, `actor_type`
   (default `agent`), `data` and `at` (a time placeholder such as `{now-5m}`; default now).
2. Write a shot spec JSON. Each shot names a path, a `ready_selector`, an optional user, optional steps
   (`{"click": selector}`, `{"hover": selector}`, `{"wait": selector}`,
   `{"type": {"selector": selector, "text": "value"}}`, `{"press": {"selector": selector, "key": "ArrowLeft"}}`,
   `{"drag": {"selector": selector, "from": [x, y], "to": [x, y]}}`), a crop (`"viewport"`, `"full"`,
   `{"selector": s, "padding": px}` or `{"clip": [x, y, width, height]}`), an optional `"color"`
   colour theme id (as in `web/src/lib/theme.ts`), an optional `"root_font"` root font size in
   pixels (for example `20`), `"themes": ["light"]` to capture only some of
   `light` and `dark` (default both) and `"narrow": true` for an extra 390-pixel-wide
   capture. Set `"signedOut": true` instead of a `user` for a signed-out page; the context has
   no session. `"before": false` skips the before capture for a page that exists only on the
   branch; `table` then shows the after shot alone.
   Start from `shots.example.json`. Use a `clip` crop when
   before and after must line up: a selector crop follows the element's size.
   Full and selector crops expand the viewport to the document height before capture.
   Viewport and explicit clip crops keep the requested desktop or narrow viewport.
   A `type` step fills the field, replacing its contents; empty text clears it. A `press` step
   sends one key to the element. A `drag` step presses the mouse at `from` and releases it at `to`,
   each an `[x, y]` fraction of the element's box; values outside 0–1 land outside it.
   Drag targets are scrolled into view and checked for stability before measuring their box.
   For trace brush captures, assert the selected range with a state-specific `ready_selector`,
   for example `.tv-range-wrap[data-open="true"] [aria-valuetext="20–60% of trace"]`.
   The overview slider remains in the DOM when closed, so its presence alone cannot prove zoom.
   To show data or errors the seeded API cannot produce (traces, metrics, a rejected request),
   add `"respond": [{"url": "**/<package>.<Service>/<Method>", "json": {...}, "status": 400, "headers": {...}}]`,
   for example `**/loco.observability.v1.ObservabilityService/QueryMetrics`.
   Matching requests get that response; `status` defaults to 200 and `headers` is optional.
   An optional `match` object narrows an entry to requests whose JSON body has those field
   values, such as `{"metricName": "k8s.pod.cpu_limit_utilization"}`. The first listed entry that
   matches a request answers it, so list narrower entries before broader ones for the same URL.
   A spec-level `"respond"` list beside `shots` applies to every shot, after the shot's own entries.
   A shot's `path` and the strings in its `respond` JSON take placeholders, filled in when the shot
   runs: `{org:acme}`, `{workspace:acme/shared}`, `{resource:acme/shared/web}` and
   `{environment:acme/shared/production}` name seeded IDs by their scenario keys, and `{now}`,
   `{now-15m}` or `{now+1h}` (units s, m, h, d) give an RFC 3339 UTC time. A `:s` or `:ms` suffix,
   as in `{now-1h:ms}`, gives epoch seconds or milliseconds; a string that is only such a placeholder
   becomes a number. An unknown key or a malformed placeholder fails the shot. The page clock
   starts at the same instant as `{now}`, so mocked times and the page's relative times agree.
   A Connect error is a status with `{"code": "invalid_argument", "message": "...", "details": [...]}`.
   `ready_selector` is a Playwright selector for an element that shows only once the page
   holds what the shot captures, such as a loaded row rather than the page heading. The shot
   waits for it after its steps, then for animations to finish.
   To capture a loader or another pending state, add `"hold_requests": ["**/Service/Method"]`
   with Playwright URL glob patterns and a `ready_selector` for that state. Matching requests
   stay pending from navigation through capture; closing the shot's browser context cancels them.
   Keep navigation and assets outside these patterns.
   To capture an intentional running animation, set `"exclude_animations"` to CSS selectors
   for its animated elements, for example `["button[aria-label=\"Hide PORT\"] circle"]`.
   Only animations on matching elements skip settling; ordinary finite animations still finish.
   To grant browser permissions before navigation, set `"permissions"`, for example
   `["clipboard-read", "clipboard-write"]`. Permissions apply only to the preview origin.
   `evidence.py check --spec F [--scenario F]` checks a spec without starting anything: RPC
   patterns that name no method, shots without `user` or `signedOut`, entries a broader entry
   answers first, unknown placeholder keys and theme names. `mise run ci:evidence` runs it first.
   Pitfalls the check cannot catch:
   - A zero-height SVG line never counts as visible; wait for its container instead.
   - A time-range preset button in a popover can time out; type into the range field instead.
   - Content below the fold of a scrolling drawer needs a `hover` step to scroll it into view.
3. Commit source changes before capturing the current HEAD; `up` rejects uncommitted files
   when its ref resolves to HEAD. Historical refs can be captured from a dirty checkout.
   `up` reuses the API and UI build of its commit from the cache in
   `~/Library/Caches/loco-ui-builds` (`~/.cache/loco-ui-builds` on Linux), which every checkout
   and agent on the machine shares. A commit missing from the cache builds once in a temporary
   worktree; concurrent runs of that commit wait for that build. The cache keeps the eight most
   recently used commits and every build a live run uses.
   Bring up both refs. Each prints the path of its state file on stdout:

   ```sh
   before=$(mise exec -- uv run --script "$E" up --ref origin/main --name before --scenario "$capture/scenario.json")
   after=$(mise exec -- uv run --script "$E" up --ref HEAD --name after --scenario "$capture/scenario.json")
   ```

   Read the `scenario note:` lines on stderr. When the old ref needs a different scenario to show the same
   screen (for example an organization the old app created on first sign-in), pass a second
   scenario file to the `before` run.
4. Shoot both into one directory:

   ```sh
   mise exec -- uv run --script "$E" shoot --state "$before" --spec "$capture/shots.json" --out "$capture/evidence"
   mise exec -- uv run --script "$E" shoot --state "$after" --spec "$capture/shots.json" --out "$capture/evidence"
   ```

   Files are `<shot>-<before|after>-<light|dark>[-narrow].png`. `shoot` refuses to overwrite a
   file that a run with another scenario wrote into the same `--out`. It also exits non-zero and
   saves nothing for a shot whose selector is missing or whose theme did not apply. Fix the spec
   or the scenario; never keep a shot that shows the wrong screen.
5. Open every PNG with Read and check it shows what the shot claims.
6. Stop each run you started:

   ```sh
   mise exec -- uv run --script "$E" down --state "$before"
   mise exec -- uv run --script "$E" down --state "$after"
   ```

   Reserve `down --all` for manual machine-wide cleanup; it stops other callers' runs.
   Each run holds a two-hour lease in `tools/testres`; every `up`, `shoot` and `down` also stops
   expired runs and leftovers whose owner is gone. A failed `up` stops itself after printing its
   API and UI logs. Runs share one Postgres server per Postgres image and one seeded OpenBao per
   OpenBao setup, each run with its own database. The registry stops a shared server once no
   live run holds it, and `down` drops the run's database. `shoot` connects to one headless
   Chromium per Playwright version that every run shares, started on demand and stopped once
   no `shoot` or suite uses it.

## Networks and OpenBao

`LOCO_TEST_SUBNET` selects an unused subnet for a Compose project, for example
`LOCO_TEST_SUBNET=10.243.17.0/24`. Omit it for Docker's automatic allocation. Use a different,
non-overlapping subnet for each concurrent project. This applies to `up`, `openbao`,
`mise run test:ui` and `mise run e2e`; keep the value when resuming a kept e2e run.
E2e reserves the Compose network before builds. Its failure reports how to list project
networks and tear down the current run. Remove only runs you own.

Every run on the machine whose OpenBao image, compose files and seed script match uses one
seeded OpenBao, started on demand and stopped with the last run that uses it. Each API keeps
application data in its own database. To use another OpenBao, set `UI_EVIDENCE_OPENBAO` to its
address before `up`; `openbao` starts one with a two-hour lease that every `up` and `shoot`
using it renews, and `stop-openbao --address "$UI_EVIDENCE_OPENBAO"` removes it.

## Upload

Upload with `gh --attach`, never through a browser. Print the tables, which reference the PNGs by
their local paths:

```sh
mise exec -- uv run --script "$E" table --out "$capture/evidence" > "$capture/evidence.md"
```

Put `$capture/evidence.md` under a `## Screenshots` heading in the PR body file, then attach every
PNG the tables reference. Relative `evidence/<file>.png` paths, as a CI capture's `evidence.md`
has, resolve against the body file's directory. `gh` uploads each file and rewrites its local path
in the body to the uploaded asset:

```sh
gh pr edit <pr> --body-file body.md --attach "$capture/evidence/<file>.png" --attach ...
```

`gh pr create --body-file body.md --attach ...` does the same for a new PR. One command takes at
most 50 attachments.

Then read `gh pr view <pr> --json body,isDraft`. No local `evidence/` path may remain, and the
draft state must be unchanged; restore a draft with `gh pr ready <pr> --undo`. If some uploads
failed, `gh` exits non-zero after updating the body with the ones that succeeded: attach only the
files whose paths are still local. Never commit the PNGs.
