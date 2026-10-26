---
description: Open one small pull request that removes code without changing behavior
on:
  schedule: weekly
  workflow_dispatch:
  skip-if-match: "is:pr is:open label:agent-simplify"
permissions:
  contents: read
  pull-requests: read
  issues: read
imports:
  - shared/loco.md
timeout-minutes: 60
safe-outputs:
  footer: false
  create-pull-request:
    draft: true
    labels: [agent-simplify]
    allowed-files:
      - "**/*.go"
    excluded-files:
      - "gen/**"
      - "**/zz_generated*.go"
      - "e2e/fixtures/**"
    max-patch-files: 8
---

# Code simplifier

Make one Go change that leaves behavior identical and the code smaller or plainer. Pick the
highest-value candidate from:

- logic duplicated across modules or packages
- hand-written parsing, matching or lookup tables the standard library already provides
  (`net/netip`, `net/url`, `slices`, `maps`, `strings`, `errors`)
- interfaces with one implementation, wrappers that only forward, options nobody sets
- concurrency or error handling more complex than the code needs
- a value maintained in two places that should come from one source

Keep the existing semantics exactly, including edge cases. A host check that treats
`localhost` differently from loopback addresses must still do so after the change.

## Constraints

- No new dependencies, no behavior change, no new abstractions.
- At most 8 files and about 200 changed lines. Leave generated code alone.
- Search open and closed pull requests labelled `agent-simplify` and skip candidates already
  proposed or rejected.
- The affected packages' tests and `mise run lint:go <path>` pass before you finish. When a
  package has no test covering the code you change, add one in the same change that passes
  before and after.

## Output

Open one draft pull request. The title follows the repository's `type(scope): summary`
convention. The body names what was removed or replaced, why the behavior is unchanged, and
which tests cover it. When no candidate meets the bar, call `noop` with the areas you checked.
