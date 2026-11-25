---
name: release
description: Cut a Loco release. Writes the release notes (a plain summary, a section per part of the app, breaking changes) from every pull request merged since the last tag, dispatches the Release workflow and checks the published notes. Load when asked to release, ship, tag or cut a new Loco version, or to write release notes.
---

# Release

The Release workflow (`.github/workflows/release.yml`) publishes one version from a commit on `main`. It takes a
`summary` input, puts it at the top of the GitHub release, and appends the generated list of merged pull requests
under it. This skill writes that summary and runs the workflow.

Load `prose` before writing the notes.

## 1. Collect the changes

```sh
git fetch origin main --tags
last=$(git tag -l 'v*' --sort=-v:refname | sed -n 1p)
git log --first-parent --format='%h %s' "$last..origin/main"
```

Every first-parent commit is one squash-merged pull request. Read the PR body of each one whose subject does not make
its user-facing effect clear (`gh pr view <n>`). Note reverts: a change reverted before the release shipped nothing,
and neither it nor its revert goes in the notes.

## 2. Write the summary

The summary has three parts, in this order.

### Summary

Two to five sentences a newcomer understands on one read, as if explaining to a five-year-old: what someone using
Loco can do now that they could not before, and what got fixed. Plain words, one idea per sentence, no flags, file
names or PR numbers.

### By part of the app

One `###` heading per part that changed, in this order, skipping parts with no user-facing change:

| Heading | Commit scopes |
| --- | --- |
| CLI | `cli`, `locoadm` |
| API | `api`, `auth`, `authz`, `events`, `webhooks`, `builds`, `deployments` |
| Dashboard | `web`, `ui` |
| Controller and agent | `controller`, `agent` |
| Install and platform | `charts`, `platform`, `installation`, `release`, an empty scope that changes the bundle |
| Observability | `observability`, `collector`, `clickhouse`, `traces`, `ingest` |
| Docs | `docs` |

Each part is a short bullet list, one change per bullet, stated as what the user sees: "`loco deploy --no-file`
deploys without reading `loco.yaml`", not "add a flag". Merge PRs that touch the same feature into one bullet.

Changes only to tests, CI, dev tooling, evidence scripts or skills (`e2e`, `tests`, `ci`, `dev`, `ui-evidence`,
`evidence`, `stress`, `skills`) are left out of the sections; they still appear in the generated list.

### Breaking changes

Every change that makes an existing install, CLI script, API client or `loco.yaml` stop working as it did, with what
the operator or user does about it. Sources:

- Commit subjects with `!` after the type or scope (`feat(charts)!:`).
- PRs labelled `break-buf` (a breaking API change).
- Edited migrations (`git diff --stat "$last..origin/main" -- '*migrations*'`): an existing database needs a reset.
- Renamed or removed chart values, CLI flags, `loco.yaml` fields or config keys found while reading the PRs.

When there are none, the section says "None."

### Format

```markdown
## Summary

Loco can now ...

## What's new

### CLI

- ...

### API

- ...

## Breaking changes

- ...
```

A blank line goes around each heading and none between bullets; a blank line between bullets makes GitHub render a
loose list with a paragraph gap per item. Claim only what is merged into the commit being released.

Show the notes to the user and wait for their go-ahead before dispatching.

## 3. Dispatch and check

Write the notes to a file in the scratchpad, then dispatch from `main`. An empty `version` bumps the patch of the
latest tag.

```sh
gh workflow run release.yml -R team-loco/loco --ref main -f version= -f summary="$(cat notes.md)"
gh run list -R team-loco/loco --workflow release.yml -L 1
```

Wait for the run with `gh run watch <run-id> --exit-status` as a background command. The run fails before publishing
if any CI check on the commit fails; diagnose the failure, merge a fix, and dispatch again from the new `main`.

Once it passes, open the release (`gh release view v<version> -R team-loco/loco --web`) and confirm the summary
renders above the generated list of pull requests.
