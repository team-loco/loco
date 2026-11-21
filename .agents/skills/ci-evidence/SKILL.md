---
name: ci-evidence
description: Run tests/ui browser suites, or take before and after screenshots of the web UI in light and dark mode, for a PR in CI with `mise run ci:evidence`. The default for both; use it while the PR is a draft. Load before running browser suites or taking PR screenshots.
allowed-tools: Bash, Read, Write
---

# CI evidence

`mise run ci:evidence` runs `tests/ui` suites and/or before/after screenshots in CI for the
pushed head of the current branch, waits for that run (5 to 15 minutes) and downloads its
output: `evidence/*.png`, `evidence.md`, `refs.txt` (the before and after commits),
`suites.log` and `suites/`.

Never hand the wait to a subagent. A subagent cannot block on a long wait: the harness moves
its long commands to the background, so it ends its turn to wait, and every ended turn wakes
the main session with an interim status update. Start the command yourself.

## Run

1. Push the branch. The task refuses to start when the local head differs from the pushed
   head. A branch made before `ci:evidence` existed must rebase onto `origin/main` first.
2. Write `shots.json` and, if needed, `scenario.json` in your scratchpad, never in the
   checkout; the task sends their contents to CI. Start from
   `.agents/skills/ui-evidence/shots.example.json`, `scenario.example.json` and
   `scenario.resources.example.json`. The ui-evidence skill's Capture locally steps 1 and 2
   describe the spec and scenario format.
3. Start the command as one background shell command (Claude Code: `Bash` with
   `run_in_background: true`) and carry on with other work. Do not poll it. Its exit
   notification is the result.

   ```sh
   mise run ci:evidence --out "$capture" --shots "$capture/shots.json" --scenario "$capture/scenario.json" [suite...]
   ```

   `--base` names the before ref (default `main`); without `--scenario` CI uses
   `scenario.example.json`. Suite names after the flags run those `tests/ui` suites in the
   same run.

Put one PR's suites and shots into one call. One call is one CI job, out of a pool PR CI also
uses.

## Result

- Exit 0: open every PNG with Read and check it shows what the shot claims. Put
  `$capture/evidence.md` under `## Screenshots` in a body file in `$capture`, then upload as the
  ui-evidence skill's Upload section describes; the table's relative `evidence/` paths resolve
  against the body file's directory. Editing the PR body starts no CI.
- Non-zero exit: read `$capture/suites.log` and the run log for `scenario note:` lines and the
  failing suite or shot, and fix the cause. Never just re-run.

A draft PR runs no CI, so use this while the PR is a draft. `gh pr ready` runs the full CI once.
