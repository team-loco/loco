---
name: troubles
description: Log every obstacle hit while working in the loco repo, as local notes the user reviews to improve the repo and the agent setup. Load whenever a task, tool, doc or environment step fails or costs extra time (a broken mise task, missing setup in a worktree, a wrong instruction, a blocked tool), and before finishing any task that hit one.
---

# Logging troubles

Trouble entries are local notes and are never committed; `.agents/troubles/` is
gitignored. Write them in the main checkout, which outlives worktrees:

```sh
dir="$(dirname "$(git rev-parse --git-common-dir)")/.agents/troubles"
```

Each obstacle gets one file in that directory, named `YYYY-MM-DD-<slug>.md`, so
concurrent agents never edit the same file.

## Entry format

```markdown
# <what broke, in a few words>

- Doing: <the task in progress>
- Symptom: <the exact error or behaviour, with the command that produced it>
- Cause: <the root cause, or "unknown">
- Workaround: <what got past it>
- Status: open | fixed in <PR>
```

## Rules

- Log it when it happens, not from memory at the end.
- Fix the cause when you can: a mise task, a skill, AGENTS.md or the code. Set Status to
  the PR that fixes it and keep the entry; the user reviews entries and deletes them.
- A fix that teaches a workaround belongs in the matching skill, so the next agent
  avoids the trouble without reading the log.
- Before starting work, skim open entries for the area you touch; one may already
  describe your obstacle.
- Never `git add` the directory or mention entries in commits, PRs or issues.
