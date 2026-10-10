# Loco Notes

Current direction and remaining work. [Checklist](checklist.txt) tracks the open items.
Audited against both repos on 2026-10-09. Implementation status only; production rollout is not verified here.

## Current direction

- Builds run remotely with rootless BuildKit. CLI uploads source; `--image` takes a public image. No local Docker daemon required.
- Registry contract is OCI. Separate builder, node and API credentials; deployments pin digests. Image retention and source cleanup exist.
- API commits desired placements; agents sync and apply them. Deployments are async; `--wait` watches rollout.
- Clusters are selected by region and environment tier. Placement does not account for utilization or pin a workspace to a cluster.
- Network isolation is implemented and tested. App namespaces are per workspace; default deny, DNS, public egress and gateway ingress policies exist. Apps within a workspace can communicate; per-app peer restrictions are not implemented.
- Environment CRUD and deployment targeting exist. `loco deploy --env` selects the environment.
- Resource and deployment specs persist `spec_version`. Only service deployment is implemented.
- CLI has org/workspace CRUD, interactive `loco use`, browser/device login and token refresh. `--host` is command-local.
- Platform domain defaults come from `ConfigService.GetConfig`. Public and private apps are supported; custom domains are rejected.
- Logs, metrics and events have UI. Resource CPU/memory charts query the regional proxy; mock usage is gone.
- Both historical logs and live tail use ClickHouse through the proxy. Tail polls ClickHouse; direct cluster tailing is no longer the design.
- Log queries support ordering, substring search, severity and labels. Log details include attributes. Queries use bound parameters.
- Audit events and signed workspace/install webhooks exist. Build logs are filtered by build ID through the same ClickHouse proxy, with history and live tail.
- UI wrappers use theme tokens. Old font stacks, large unused assets and dead pages are gone. CI runs actionlint on workflow changes.
- Docs use Zensical and ship with the UI image. `loco.toml` is current; the [`loco.yaml` cutover](https://github.com/team-loco/loco/pull/538) is under review.

Sources: `cmd/loco/`, `api/service/`, `api/queries/`, `controller/internal/`,
`observability-proxy/`, `web/src/pages/`, `.github/workflows/`, `docs/content/`.

## V1

### Networking and secrets

- Decide whether workspace isolation needs per-app peer restrictions.
- Pin placement per workspace, region and tier. Apps can span regions; keep workspace peers together within each region/tier.
- Ship the [environment-secrets stack](https://github.com/team-loco/loco/pull/504): encrypted storage, key rotation and agent/controller delivery. Platform credential rotation remains separate; revisit the older [OpenBao proposal](tdd/secrets-handling.md).
- Decide on in-cluster mTLS.

### Observability

- Verify tenant attributes on every collected record. Workspace/resource query filters exist; collection still needs coverage.
- Restrict collection to managed workloads. Drop unnecessary metrics and high-cardinality attributes.
- Replace exporter-generated tables with indexed tenant queries and explicit retention.
- Align advertised app retention with collector TTLs. Global data TTL and ClickHouse system-log TTL exist; per-tenant retention does not.
- Parse severity from structured app logs.
- Validate dashboard values against emitted telemetry. Add disk metrics.
- Purge logs, metrics and traces on app/workspace deletion. Make cleanup retryable after outages.
- Add pause/freeze to CLI log output.

### Deploy and builds

- Finish CI authentication for deploy: accept an explicit token without an OS keychain. Scope/environment flags already avoid most prompts.
- Test failed deploy cleanup and define rollback behavior. DB writes are transactional; multi-region rollout is not atomic.
- Persist secret versions for rollback.
- Bound concurrent app rollouts.
- Enforce size limits on public images; built images already have a size cap.
- Verify gRPC ingress end to end.
- Ship `loco.yaml`, environment overrides, partial ownership, plan/apply and deploy. Replace the Go-definition docs; the earlier Go SDK stack is closed. Keep local linking and local/deployed diff work with this cutover.

### API and data

- Audit internal error responses; use the shared DB error handling throughout.
- Audit remaining request validation gaps. The validation interceptor and many proto rules already exist.
- Review CRUD response contracts; several mutations already return IDs.
- Review remaining multi-step writes and ordering indexes. Update uniqueness checks exclude the current row; core pagination indexes exist.
- Review Kubernetes ownership and cleanup of generated objects.
- Add invitations. Member listing and scope management already exist.

### Infrastructure

- Manage worker chart/CRD upgrades and drift. [Flux design](tdd/flux-gitops.md) remains proposed; Argo evaluation is already covered there.
- Finish chart configuration and CRD lifecycle separation. Operator CRDs still ship in chart templates.
- Add capacity-aware placement. Current selection uses region, tier, health and default preference.
- Configure node autoscaling and Envoy Gateway HPA.
- Load-test ingress, API, builds and deployments. Measure minimum footprint and capacity headroom.
- Implement API/deploy rate limits; observability query/tail guardrails already exist.
- Refresh [dependency map](dependencies.md), especially builds, registry, auth and observability.
- Validate networking upgrades against the configured socketLB and datapath settings.
- Finish [regional failover](https://github.com/team-loco/loco/pull/134); [the mechanism is proven](tdd/cross-region-failover.md). Still need gateway TLS, outage detection and surviving-region capacity.

### Data and verification

- Finish deletion across Kubernetes, telemetry, secrets and images. Placement deletion and image sweeping already exist.
- Test Postgres and ClickHouse backup/restore.
- Extend failure-path coverage for deploy and platform rollout. API DB, CLI script, controller and build e2e suites already exist.
- Add dashboard Playwright coverage.

### Tooling

- Consolidate `tsgo` and `tsc` once the ESLint toolchain supports the native compiler. Both checks still run today.
- Finish the `cel-go` module-path migration outside API. API already uses `cel.dev/cel-go`; controller still uses the old module.

## V2

### Observability

- Collect traces, enforce tenant scope and build trace UI. Current collectors have no traces pipeline; the UI is a placeholder.
- Provision per-workspace Grafana dashboards and alerts if users need them.
- Evaluate external ClickHouse without changing the proxy contract.

### Networking and builds

- Scan images and add artifact attestations. [Scanning design](tdd/scanning.md).
- Add per-app egress controls.
- Add custom domains and certificate lifecycle.
- Add build-only cluster placement. `builds.enabled` controls builders; app placement has no accepts-apps switch.
- Add non-HTTP health checks.
- Add app sleep/wake.
- Add service packages, recursive deploy and package deletion.

### Infrastructure and platform

- Rebuild clusters from tested snapshots/backups.
- Define multi-cluster certificate ownership.
- Define infra patch/upgrade strategy, including node replacement.
- Add bring-your-own-cloud profiles.
- Add admin dashboard and status page.
- Add database, cache and blob resources.
- Add persistent disks per service.
- Evaluate frontend analytics.
- Integrate external secret stores.

### Data model

- Decide whether to replace UUIDs. New IDs already use UUIDv7.
- Split sqlc queries into packages if the boundaries warrant it.
- Add per-tenant rate limits and retention for audit/webhook records.

## V3

- Clean up inactive accounts and unused resources.
- Add canary deployments.
- Export Kubernetes YAML so users can move to self-managed infra.
- Define supported Kubernetes minors and test controller/agent compatibility in CI.

## Backlog (only if users ask)

- Minimal init output; revisit with the `loco.yaml` cutover.
- `NO_COLOR` / `--no-color` and plain terminal output.
- Alternative remote builders.
- Revisit protobuf serialization optimizations when supported by the toolchain.

## Principles

- Reduce package depth.
- Prefer stdlib and established Go/Kubernetes packages.
- Define dependency patching ownership and response times.
