---
name: ui-evidence
description: Capture before and after screenshots of the web UI, in light and dark mode, for a PR that changes what the web UI looks like or how it behaves. Takes them in CI by default with `mise run ci:evidence`, or locally: builds each git ref in its own worktree with a throwaway database, OpenBao, API and UI, seeds users, sessions and organization memberships with SQL and organizations, workspaces and grants through the API, and shoots headless Chromium. Load before taking PR screenshots.
context: fork
agent: ui-evidence
allowed-tools: Bash, Read, Write
---

# UI evidence

`scripts/evidence.py` does every brittle step. CI runs it for a pushed branch; locally, run it
with uv from the repository root. It never touches the dev stack (`mise run dev`), Dex or a
person's browser windows.

## Capture in CI

This is the default. It keeps the browsers, databases and builds off your machine.

1. Open the PR as a draft (`gh pr create --draft`). A draft runs no CI, so pushes cost nothing.
2. Write `scenario.json` and `shots.json` into a directory outside the checkout, as steps 1
   and 2 of [Capture locally](#capture-locally) describe. They are not committed; the task
   sends their contents to CI.
3. Push the branch, then run the capture for its pushed head:

   ```sh
   mise run ci:evidence --out "$capture" --shots "$capture/shots.json" --scenario "$capture/scenario.json"
   ```

   `--base` names the before ref (default `main`); without `--scenario` CI uses
   `scenario.example.json`. Suite names after the flags also run those `tests/ui` suites in
   the same run. The task refuses to start when the local head differs from the pushed one,
   prints the run URL, checks it every 30 seconds and downloads `evidence/*.png`,
   `evidence.md`, `refs.txt` (the before and after commits) and any suite output into
   `$capture`. It exits non-zero when the run failed; read the run log for `scenario note:`
   lines and the failing shot.
4. Open every PNG with Read and check it shows what the shot claims.
5. Write the PR body to `$capture/body.md` with `$capture/evidence.md` under `## Screenshots`,
   and upload as [Upload](#upload) describes. The table's `evidence/<file>.png` paths resolve
   against the body file's directory.
6. Mark the PR ready (`gh pr ready`); that runs the full CI.

## Capture locally

Use this to click through a running app: `up` leaves each ref's API and UI running until `down`.

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
   Start from `.agents/skills/ui-evidence/scenario.resources.example.json`. Resources use the API; synthetic
   clusters, deployments and placements use SQL without contacting a registry or worker.
   The evidence API uses the two-hour run lease as its heartbeat interval; synthetic clusters
   stay live for six hours after seeding, covering long light/dark and narrow capture batches.
   The state file's `resources` maps `org/workspace/name` to resource IDs for shot paths, and
   `orgs` maps each organization name to its `id` and `owner`.
2. Write a shot spec JSON. Each shot names a path, a `ready_selector`, an optional user, optional steps
   (`{"click": selector}`, `{"hover": selector}`, `{"wait": selector}`,
   `{"type": {"selector": selector, "text": "value"}}`, `{"press": {"selector": selector, "key": "ArrowLeft"}}`,
   `{"drag": {"selector": selector, "from": [x, y], "to": [x, y]}}`), a crop (`"viewport"`, `"full"`,
   `{"selector": s, "padding": px}` or `{"clip": [x, y, width, height]}`), an optional `"color"`
   colour theme id (as in `web/src/lib/theme.ts`) and `"narrow": true` for an extra 390-pixel-wide
   capture. Omit `user` for signed-out pages; the context has no session.
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
   add `"respond": [{"url": "**/Service/Method", "json": {...}, "status": 400, "headers": {...}}]`.
   Matching requests get that response; `status` defaults to 200 and `headers` is optional.
   An optional `match` object narrows an entry to requests whose JSON body has those field
   values, such as `{"metricName": "k8s.pod.cpu_limit_utilization"}`; other requests fall
   through to the next entry for the same URL.
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
3. Commit source changes before capturing the current HEAD; `up` rejects uncommitted files
   when its ref resolves to HEAD. Historical refs can be captured from a dirty checkout.
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
   Each run holds a two-hour lease in `tools/testres`, the registry every local test harness
   shares; its containers, processes, worktree and ports are recorded under that lease, outside
   any temporary directory. Every `up`, `shoot` and `down` also stops runs nobody has used for
   two hours and other harnesses' leftovers whose owner is gone. A failed `up` stops itself after
   printing its API and UI logs, so an interrupted capture does not leave containers behind.

Return the PNG paths, one line per file saying what it shows, every `scenario note:` line and every
failure.

## Networks and shared OpenBao

`LOCO_TEST_SUBNET` selects an unused subnet for a Compose project, for example
`LOCO_TEST_SUBNET=10.243.17.0/24`. Omit it for Docker's automatic allocation. Use a different,
non-overlapping subnet for each concurrent project. This applies to `up`, `openbao`,
`mise run test:ui` and `mise run e2e`; keep the value when resuming a kept e2e run.
E2e reserves the Compose network before builds. Its failure reports how to list project
networks and tear down the current run. Remove only runs you own.

Comparisons can share one seeded OpenBao. Each API keeps application data in its own
database. Build each ref in a separate checkout and keep both checkouts until capture
finishes: each UI serves its checkout's `web/dist`. From the checkout with this script:

```sh
E="$PWD/.agents/skills/ui-evidence/scripts/evidence.py"
git worktree add --detach "$capture/before-src" origin/main
git worktree add --detach "$capture/after-src" HEAD
UI_EVIDENCE_OPENBAO=$(mise exec -- uv run --script "$E" openbao)
export UI_EVIDENCE_OPENBAO
for side in before after; do
  (
    cd "$capture/$side-src"
    mise exec -- uv run --script "$E" build
    mise exec -- uv run --script "$E" up --ref HEAD --name "$side" --scenario "$capture/scenario.json"
  ) > "$capture/$side.state-path"
done
before=$(cat "$capture/before.state-path")
after=$(cat "$capture/after.state-path")
```

Shoot with those state paths and the spec above. `UI_EVIDENCE_OPENBAO` makes `up` use the
current checkout's built API and UI; it does not build or check out `--ref`. The shared
OpenBao has a two-hour lease like a run's. Every `up` and `shoot` that uses it renews that
lease, so it stays up while its captures run, whichever shell started it. The caller owns these
checkouts and the shared OpenBao. After capture:

```sh
mise exec -- uv run --script "$E" down --state "$before"
mise exec -- uv run --script "$E" down --state "$after"
mise exec -- uv run --script "$E" stop-openbao --address "$UI_EVIDENCE_OPENBAO"
unset UI_EVIDENCE_OPENBAO
git worktree remove --force "$capture/before-src"
git worktree remove --force "$capture/after-src"
```

## Upload

Upload with `gh --attach`, never through a browser. Print the tables, which reference the PNGs by
their local paths:

```sh
mise exec -- uv run --script "$E" table --out "$capture/evidence" > "$capture/evidence.md"
```

Put `$capture/evidence.md` under a `## Screenshots` heading in the PR body file, then attach every PNG the
tables reference. `gh` uploads each file and rewrites its local path in the body to the uploaded
asset:

```sh
gh pr edit <pr> --body-file body.md --attach "$capture/evidence/<file>.png" --attach ...
```

`gh pr create --body-file body.md --attach ...` does the same for a new PR. One command takes at
most 50 attachments.

Then read `gh pr view <pr> --json body,isDraft`. No local `evidence/` path may remain, and the
draft state must be unchanged; restore a draft with `gh pr ready <pr> --undo`. If some uploads
failed, `gh` exits non-zero after updating the body with the ones that succeeded: attach only the
files whose paths are still local. Never commit the PNGs.
