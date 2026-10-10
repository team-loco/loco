# Loco Notes

What works and what is left. [Checklist](checklist.txt) lists open work.
Checked against both repos on 2026-10-09. Production rollout was not checked.

## What works

- CLI uploads source; rootless BuildKit builds it on Loco. No local Docker daemon needed. `--image` deploys a public image.
- Builds push to an OCI registry. Builder, node and API accounts have separate permissions. Deploys use image digests; old images and source uploads get cleaned up.
- API saves what should run; agents apply it to their clusters. Deploys return before rollout finishes; `--wait` waits for it.
- One namespace per workspace. Apps can talk to other apps in the same workspace by default; cross-workspace traffic is blocked.
- Clusters are picked by region and environment type. Free capacity and keeping workspace apps together are still missing.
- Environments can be created, listed, updated and deleted. `loco deploy --env` picks one.
- App and deployment configs store `spec_version`. Only services can be deployed today.
- CLI manages orgs/workspaces. `loco use` has an interactive picker; login supports browser/device flows and token refresh. `--host` is set on individual commands.
- API provides the default platform domain. Apps can be public or private; custom domains are still rejected.
- UI shows logs, metrics and events. CPU/memory charts use real queries; mock usage is gone.
- Log history and live tail both read ClickHouse through the observability proxy. Logs support search, ordering, severity/label filters and viewing all fields.
- Audit events and signed webhooks exist. Build logs use a build-ID filter through the same proxy.
- UI uses theme tokens. Old fonts, unused assets and dead pages are gone. CI checks workflow changes with actionlint.
- Zensical docs ship in the UI image. `loco.toml` is current; the [`loco.yaml` replacement](https://github.com/team-loco/loco/pull/538) is under review.

## V1

### Networking and secrets

- Keep apps in the same workspace, region and environment on the same cluster so they can talk to each other.
- Encrypt app secrets and support rotating encryption keys. The [secrets PRs](https://github.com/team-loco/loco/pull/504) are still open.
- Set up rotation for platform credentials. Revisit the older [OpenBao proposal](tdd/secrets-handling.md) for the current auth and registry setup.
- Decide whether connections inside a cluster need mutual TLS.

### Observability

- Check that logs and metrics include the workspace and app IDs. Queries already filter by those IDs; check what the collectors actually send.
- Only collect logs from Loco apps; stop collecting metrics we do not use. Remove labels with too many distinct values.
- Create ClickHouse tables with indexes for workspace/app queries. Tables are still created automatically by the exporter.
- Make logs expire after the retention period shown to users. Collector TTLs exist, but they do not enforce each app’s setting.
- Read log severity from structured app logs.
- Check dashboard numbers against real app usage; add disk metrics.
- Delete logs, metrics and traces when an app or workspace is deleted. Retry failed cleanup after outages.
- Let users pause CLI log output.

### Deploy and builds

- Let CI deploy with a token, without requiring an OS keychain. Org/workspace/environment flags already avoid most prompts.
- Test cleanup after failed deploys; decide what rollback restores. DB writes use transactions, but deploying to several regions can leave some updated and others failed.
- Keep old secret versions so rollback can restore them.
- Limit how many apps can deploy at once.
- Limit the size of public images users can deploy. Images built by Loco already have a size limit.
- Test deploying and calling a gRPC app through Envoy Gateway.
- Finish deploying from `loco.yaml`, including environment overrides and reviewing changes before applying them. The [PRs are open](https://github.com/team-loco/loco/pull/538). Include deploying subsets of services, linking local files to a workspace and showing local/deployed differences. Replace the older Go-definition docs.

### API and data

- Add admin/SRE APIs to pause and resume deploys and manage cluster maintenance. Allow controls for one cluster, one region or all clusters. Decide what happens to queued and running deploys; keep running apps up. Save controls across restarts and record who changed them.
- Check API errors for raw database details; return useful user-facing errors. Shared DB error handling already exists.
- Check that every API rejects invalid requests. Many proto validation rules already exist.
- Make create/update responses consistent about returning an ID or the full object. Several endpoints already return only an ID.
- Check remaining DB writes use transactions and sorted queries have indexes. Update name checks already exclude the current row; main list queries have indexes.
- Check that deleting an app removes the Kubernetes objects it created.
- Add invitations. Member listing and permissions already exist.

### Infrastructure

- Set up worker cluster upgrades and check that each cluster runs the expected charts and CRDs. The [Flux proposal](tdd/flux-gitops.md) already compares Argo.
- Make chart settings configurable and install CRDs separately. Operator CRDs still ship inside the chart.
- Choose a cluster with enough free CPU and memory for the app. Selection currently checks region, environment type and health, then prefers the default cluster.
- Scale cluster nodes and Envoy Gateway replicas as traffic grows.
- Load-test the API, ingress, builds and deploys. Measure idle resource use and how much spare capacity is needed.
- Limit API requests, especially deploy requests. Log/metric queries and live tails already have limits.
- Update the dependency list for builds, registry, auth and observability. [Current list](dependencies.md).
- Test Cilium upgrades with our socketLB and datapath settings.
- Finish routing traffic to another region when a region goes down. The [failover PR](https://github.com/team-loco/loco/pull/134) is open. Still need TLS between gateways, a way to distinguish outages from bad deploys, and enough capacity in the other region.

### Data and testing

- Check app/workspace deletion removes Kubernetes objects, secrets, images and stored telemetry. Agent deletion and image cleanup already exist.
- Test backing up and restoring Postgres and ClickHouse.
- Add tests for failed app deploys and failed platform upgrades. API DB tests, CLI script tests, controller tests and build e2e tests already exist.
- Test dashboard flows with Playwright.

### Tooling

- Use one TypeScript compiler once ESLint supports it. We still run both `tsgo` and `tsc`.
- Update remaining modules to the new cel-go import path. API uses `cel.dev/cel-go`; controller still uses the old path.

## V2

### Observability

- Collect traces and show them in the UI, filtered by workspace/app. Collectors have no traces pipeline yet; the UI is a placeholder.
- Create Grafana dashboards and alerts for each workspace if users need them.
- Evaluate hosted ClickHouse. The observability proxy already takes a configurable ClickHouse URL.

### Networking and builds

- Scan images for vulnerabilities and record where they were built. [Scanning proposal](tdd/scanning.md).
- Let users choose which external addresses an app can connect to.
- Support custom domains and issue/renew their certificates.
- Run builds on separate clusters; prevent apps from being deployed there. `builds.enabled` controls builders, but there is no setting to reject app deployments on a build cluster.
- Support command-based health checks.
- Sleep idle apps and wake them when a request arrives.
- Deploy and delete a group of services together; support recursive deploy.

### Infrastructure and platform

- Rebuild a lost cluster from backups.
- Decide which cluster issues and renews certificates for each region.
- Define how to patch platform services and replace cluster nodes.
- Let users configure their own cloud accounts.
- Build an admin dashboard and status page.
- Support databases, caches and blob storage.
- Support persistent disks for apps.
- Decide whether to add frontend analytics.
- Let apps use secrets from AWS SSM, Vault and other secret stores.

### Data model

- Decide whether to replace UUIDs. New IDs already use UUIDv7.
- Split sqlc queries into packages where it makes the code easier to use.
- Limit requests per customer and expire old audit events and webhook records.

## V3

- Clean up inactive accounts and unused resources.
- Roll out new versions to a few instances before updating all of them.
- Export Kubernetes YAML so users can move to their own infrastructure.
- Choose supported Kubernetes versions and test the controller/agent against each one.

## Backlog (only if users ask)

- Add a smaller init template. Revisit with `loco.yaml`.
- Support `NO_COLOR` / `--no-color` and plain terminal output.
- Support other remote image builders.
- Revisit faster protobuf encoding when the tooling supports it.

## Principles

- Keep package nesting shallow.
- Prefer stdlib and established Go/Kubernetes packages.
- Assign owners and response times for dependency security fixes.
