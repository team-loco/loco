---
description: Find a distributed-system failure mode across the API, agent and controller and prove it with a regression test
on:
  schedule: weekly
  workflow_dispatch:
  skip-if-match: "is:pr is:open label:agent-reliability"
permissions:
  contents: read
  pull-requests: read
  issues: read
imports:
  - shared/loco.md
checkout:
  fetch-depth: 0
timeout-minutes: 90
safe-outputs:
  footer: false
  create-pull-request:
    draft: true
    labels: [agent-reliability]
    allowed-files:
      - "agent/**/*.go"
      - "api/**/*.go"
      - "builder/**/*.go"
      - "controller/**/*.go"
      - "internal/**/*.go"
      - "k8sapi/**/*.go"
      - "**/testdata/**"
    excluded-files:
      - "gen/**"
      - "**/zz_generated*.go"
    max-patch-files: 10
  create-issue:
    max: 2
    labels: [agent-reliability]
    deduplicate-by-title: true
---

# Reliability audit

Find one failure mode where Loco's state can become wrong or stuck, and prove it.

Loco's control path runs from the API (`api/`) through the in-cluster agent (`agent/`) to the
controller (`controller/`) and Kubernetes. Start from the code that changed most in the last
seven days (`git log --since=7.days --stat`), then look for:

- the API crashing after it persists deployment state but before it dispatches the work
- an agent disconnecting during reconciliation, then reconnecting
- duplicate or reordered commands after a reconnect
- concurrent updates to the same application or environment
- a resource deleted while its build or rollout is in flight
- stale cluster heartbeats leading to a wrong placement decision
- Kubernetes watches restarting, or a reconcile getting a conflict on update

## Bar for a finding

A finding has a concrete sequence of events, the invariant it violates, and a deterministic
test that fails on `main` because of it. No sleeps or timing races in the test; drive the
order of events explicitly. Before reporting, search open and closed issues and pull requests
labelled `agent-reliability` (`gh search issues --repo ${{ github.repository }} label:agent-reliability`)
and skip anything already reported.

## Output

- When you can fix it with a small change, open one draft pull request with the failing test
  and the fix. Run the affected package's tests and `mise run lint:go <path>` before you
  finish. The title follows the repository's `type(scope): summary` convention. The body
  states the failure sequence, the invariant, and how the test reproduces it.
- When the fix needs a design decision, open an issue with the same content and the failing
  test inline instead.
- When nothing meets the bar, call `noop` with the areas you checked.
