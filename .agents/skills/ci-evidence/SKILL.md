---
name: ci-evidence
description: Run tests/ui browser suites, or take before and after screenshots of the web UI in light and dark mode, for a PR in CI with `mise run ci:evidence`. The default for both. Load before running browser suites or taking PR screenshots.
allowed-tools: Bash, Read, Write
---

# CI evidence

`mise run ci:evidence` runs `tests/ui` suites and/or before/after screenshots in CI for the
pushed head of the current branch, waits for that run (5 to 15 minutes) and downloads its
output: `evidence/*.png`, `evidence.md`, `refs.txt` (the before and after commits),
`suites.log` and `suites/`.

Run it yourself, never in a subagent: a subagent ends its turn to wait out a long command, and
each ended turn wakes the main session with an interim update. This skill does not fork.

## Run

1. Push the branch. The task refuses to start when the local head differs from the pushed
   head. A branch made before `ci:evidence` existed must rebase onto `origin/main` first.
2. Write `shots.json` and, if needed, `scenario.json` in your scratchpad, never in the
   checkout; the task sends their contents to CI. Read steps 1 and 2 of Capture locally in
   `.agents/skills/ui-evidence/SKILL.md` for the format (the skill itself forks, so read the
   file) and start from the `*.example.json` files beside it.
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
  Upload section of `.agents/skills/ui-evidence/SKILL.md` describes. Editing the PR body starts no CI.
- Non-zero exit before a run starts: the spec check found mistakes, each naming its shot and
  field. Fix them; the pitfalls the check cannot catch are listed in the ui-evidence spec section.
- Non-zero exit after a run: the task prints `$capture/failure.md`, each failing suite or shot with
  its error lines and last log lines. Fix the cause. Never just re-run.
