---
description: Challenge the assumptions a pull request rests on and report only evidenced failures
on:
  pull_request:
    types: [opened, reopened, ready_for_review]
    paths:
      - "agent/**"
      - "api/**"
      - "builder/**"
      - "cmd/**"
      - "controller/**"
      - "internal/**"
      - "k8sapi/**"
      - "observability-proxy/**"
      - "charts/**"
      - "proto/**"
if: >-
  github.event.pull_request.head.repo.full_name == github.repository &&
  github.event.pull_request.draft == false &&
  !startsWith(github.event.pull_request.head.ref, 'renovate/') &&
  !endsWith(github.actor, '[bot]')
permissions:
  contents: read
  pull-requests: read
  issues: read
imports:
  - shared/loco.md
timeout-minutes: 45
safe-outputs:
  footer: false
  create-pull-request-review-comment:
    max: 8
  submit-pull-request-review:
    max: 1
    allowed-events: [COMMENT]
---

# Adversarial review

Review pull request #${{ github.event.pull_request.number }}.
Your job is to disprove the author's assumptions, not to summarize or praise the change.
Read the diff with `gh pr diff`, the description with `gh pr view`, and the surrounding code
in the checkout.

For each change, ask:

- Every network call succeeds but responses arrive out of order or twice. What breaks?
- Does it assume a single API replica, a single region, or one agent per cluster?
- Does a successful API response imply the state is durable, or can a crash between the write
  and the dispatch leave it half done?
- Can a reconciler touch resources of an application, environment or organization other than
  the one it reconciles?
- Could Kubernetes garbage collection, owner references or finalizers behave differently from
  what the code assumes?
- Does the test fail without the change, and does it exercise the behavior or only a mock of
  the implementation?
- Is each new abstraction, option or wrapper needed by this change?

Report a finding only when you can show it: the file and line, the concrete sequence of events
that breaks, and the invariant it violates. Run a test or write a throwaway one in the
checkout when that settles a question. Leave out style notes, naming preferences, and anything
`golangci-lint`, `buf` or the existing CI already catches.

## Output

- Post each finding as a review comment on the line it concerns, at most eight, most severe
  first.
- Then submit one `COMMENT` review whose body lists the findings in one line each. Keep it
  plain and factual; strangers read it.
- When nothing survives this bar, call `noop` with a one-sentence reason instead of posting
  anything.
