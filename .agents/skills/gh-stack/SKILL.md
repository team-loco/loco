---
name: gh-stack
description: Create, extend, restack and repair stacked PRs with `gh stack` in the loco repo. Load before running any `gh stack` command, opening PRs that build on each other, or editing or pushing to a PR that is part of a stack.
---

# Stacked PRs with gh stack

## Create a stack

Work in a worktree off `origin/main` (see AGENTS.md). Commit the bottom change, then:

```sh
gh stack init <bottom-branch>
gh stack add <next-branch>
gh stack submit --auto
```

`gh stack add` creates the next branch on top of the current one and checks it out, so
it must run from a branch that is already in the stack. To adopt a branch that already
exists, check out the bottom branch and run `gh stack add <existing-branch>`. With
`--auto`, `submit` opens new PRs as drafts.

## Extend a stack checked out in another worktree

`gh stack checkout <pr>` checks out the stack's top branch, and git refuses a branch
that another worktree has checked out, so the checkout fails. Build the new PRs without
local stack tracking:

```sh
git switch -c <new-branch> origin/<top-branch>
git push -u origin <new-branch>
gh pr create --draft --base <top-branch>
gh stack link <stack-number> <pr-number>...
```

## Restack after amending a lower branch

After amending a branch whose parent has merged, run
`GIT_EDITOR=true gh stack rebase --upstack --no-trunk` from the branch above the amended
one. Run from the amended branch itself, it rebases that branch onto the trunk head the
stack recorded and replays main's history as conflicting picks.
`gh stack rebase --abort` restores every branch.

## Reorder or retarget PRs

GitHub pins the base of a PR in a stack:

- `gh pr edit <pr> --base <branch>` fails with `Cannot change the base branch because the
  pull request is part of a stack`, even when the PR is closed.
- `gh stack link` only appends to the top: `new PRs must be added to the top of the
  existing stack`. A closed PR at the top blocks every later link.
- Renaming a PR's head branch closes the PR. Open PRs whose base is the renamed branch
  follow the rename.

To insert a PR in the middle, force-push the new contents onto the existing branches so
each PR shifts one position, retitle them, and link the old top commit as a new PR. To
retarget one open PR, push the commit it should build on to a scratch branch,
`git push origin --delete <old-base>`, then rename the scratch branch to `<old-base>`.

## Drafts

An edit or push to a draft PR can mark it ready: `gh pr edit --body-file` and
`git push --force-with-lease` both flipped drafts. After either, check
`gh pr view <pr> --json isDraft` and run `gh pr ready <pr> --undo` if it changed.
