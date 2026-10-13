---
name: ui-evidence
description: Capture before and after screenshots of the web UI, in light and dark mode, for a PR that changes what the web UI looks like or how it behaves (AGENTS.md "Frontend PRs carry before and after screenshots"). Builds each git ref in its own worktree with a throwaway database, API and UI, seeds users through the API, and shoots headless Chromium. Load before taking PR screenshots.
context: fork
agent: ui-evidence
allowed-tools: Bash, Read, Write
---

# UI evidence

`scripts/evidence.py` does every brittle step. Run it with uv from the repository root; it
never touches the dev stack (`mise run tilt`), Dex or a person's browser windows.

```sh
E="${CLAUDE_SKILL_DIR}/scripts/evidence.py"
mise exec -- uv run --script "$E" <command>
```

## Capture

1. Write a scenario JSON: the users, the organizations each owner creates with their
   workspaces and members, and workspace grants. Start from `scenario.example.json`.
2. Write a shot spec JSON. Each shot names a user, a path, optional steps
   (`{"click": selector}`, `{"wait": selector}`), a crop (`"viewport"`, `"full"`,
   `{"selector": s, "padding": px}` or `{"clip": [x, y, width, height]}`) and `"narrow": true`
   for an extra 390-pixel-wide capture. Start from `shots.example.json`. Use a `clip` crop when
   before and after must line up: a selector crop follows the element's size.
3. Bring up both refs. Each prints the path of its state file on stdout:

   ```sh
   before=$(mise exec -- uv run --script "$E" up --ref origin/main --name before --scenario scenario.json)
   after=$(mise exec -- uv run --script "$E" up --ref HEAD --name after --scenario scenario.json)
   ```

   Read the `scenario note:` lines on stderr. A note means the ref lacks an API the scenario uses and the
   script left that part out. When the old ref needs a different scenario to show the same
   screen (for example an organization the old app created on first sign-in), pass a second
   scenario file to the `before` run.
4. Shoot both into one directory:

   ```sh
   mise exec -- uv run --script "$E" shoot --state "$before" --spec shots.json --out evidence/
   mise exec -- uv run --script "$E" shoot --state "$after" --spec shots.json --out evidence/
   ```

   Files are `<shot>-<before|after>-<light|dark>[-narrow].png`. `shoot` exits non-zero and
   saves nothing for a shot whose selector is missing or whose theme did not apply. Fix the spec
   or the scenario; never keep a shot that shows the wrong screen.
5. Open every PNG with Read and check it shows what the shot claims.
6. Stop everything: `mise exec -- uv run --script "$E" down --all`.

Return the PNG paths, one line per file saying what it shows, every `scenario note:` line and every
failure.

## Upload

Upload with `gh --attach`, never through a browser. Print the tables, which reference the PNGs by
their local paths:

```sh
mise exec -- uv run --script "$E" table --out evidence > evidence.md
```

Put `evidence.md` under a `## Screenshots` heading in the PR body file, then attach every PNG the
tables reference. `gh` uploads each file and rewrites its local path in the body to the uploaded
asset:

```sh
gh pr edit <pr> --body-file body.md --attach evidence/<file>.png --attach ...
```

`gh pr create --body-file body.md --attach ...` does the same for a new PR. One command takes at
most 50 attachments.

Then read `gh pr view <pr> --json body,isDraft`. No local `evidence/` path may remain, and the
draft state must be unchanged; restore a draft with `gh pr ready <pr> --undo`. If some uploads
failed, `gh` exits non-zero after updating the body with the ones that succeeded: attach only the
files whose paths are still local. Never commit the PNGs.
