# Go infrastructure verification

Implementation branch: `iac/14-git-workflow-docs`.

The protobuf SDK revision replaces handwritten configuration structs with shared generated aliases, moves application defaults to the API, and keeps evaluation/decoding/validation utilities outside the public SDK. CPU, logging, metrics, tracing and autoscaling remain supported. The test and browser results below describe the earlier implementation; revision checks cover compilation, generation, typechecking and lint. Updated behavioral results are tracked by the PR CI runs.


The API reset is intentional for a zero-user application. The PR uses the repository’s existing `break-buf` compatibility-check exception.

## Automated checks

The CLI, API and web production builds pass. The Go SDK and infrastructure evaluation tests pass. Database-backed API tests run against an isolated PostgreSQL 18 container; each fixture creates and drops its own database. Agent tests pass.

The implementation includes tests for context delivery, discovery, strict JSON, helper compilation, execution cancellation, path containment, tracked-source snapshots, authoring roundtrips, independent environment instances, stack permissions, no-op plans, stale plans, altered reviewed operations, apply retries, transaction rollback, selective apply, pruning, encrypted variables before deployment, redacted plans and registry publishing.

Registry tests use a local TLS fixture for upstream authentication and upload forwarding. They check target selection, credential handling, upload locations and revocation.

Commands used:

```sh
mise run build
mise run build:api
mise run build:web
mise run test:cli
mise run test:sdk
mise run test:infra
mise run test:api
mise run test:agent
mise run lint:go . api sdk/go k8sapi
mise run lint:web
mise run lint:proto
mise run lint:actions
```

The example Git workflow is checked separately with actionlint. Generated protobuf and SQL code is produced with `mise run gen`.

## Browser evidence

Browser review uses synthetic API fixtures, with no live account or deployment credentials. Screenshots cover light and dark desktop views and a narrow viewport. Fixtures include an undeployed service belonging to another environment to check dashboard isolation. Variable editing checks invalid names, masked input, a failed request, retained input and a successful retry.

Before and after screenshots are displayed in [the web PR description](https://github.com/team-loco/loco/pull/315). The HTML technical design document is standalone and was reviewed in desktop, mobile and dark layouts.

The design-context linter reports zero errors and warnings. The repository-wide static UI audit records existing findings in unrelated screens and vendored controls; it is not evidence of application-wide accessibility compliance.

## Release prerequisites and limits

- Initialize a fresh database. Initial migrations intentionally define the new ownership model without a compatibility path.
- Configure a stable base64-encoded 32-byte `INFRA_ENCRYPTION_KEY` across API replicas and retain it with database backups.
- Publish the Go SDK tag `sdk/go/v0.1.0` before releasing a CLI whose generated definitions require it.
- Configure Git workflow variables, protected deployment environments, stack credentials and a checksum-pinned released CLI before activating the example.
- Kubernetes rollout happens after desired state commits. A runtime failure does not roll back that database transaction.
- Live GitLab publishing, the hosted Git merge workflow and a full Kubernetes end-to-end deployment require configured infrastructure and are not claimed by the fixture checks.
