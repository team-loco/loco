# Architecture

Loco separates its control plane from the regional components that run applications. The CLI and dashboard call the API, and the regional agent connects the cluster to the control plane.

## Control plane

The Go API exposes ConnectRPC services and stores platform state in PostgreSQL. The CLI uses Cobra and Charm libraries; the dashboard lives in `web/`. Protocol definitions live in `proto/`, with generated clients in `gen/`.

## Regional runtime

The in-cluster agent exchanges state with the API. The controller reconciles Application custom resources into Kubernetes workloads and routing resources. Cilium provides networking; Envoy Gateway handles ingress and TLS termination; cert-manager provisions certificates.

## Observability

OpenTelemetry collects telemetry and ClickHouse stores observability data. The regional observability proxy provides access to the regional data.

Log message search is case sensitive and matches literal substrings. Percent signs, underscores and backslashes match themselves.

The observability proxy owns the ClickHouse schema. Before it serves, it applies the versioned migrations in `observability-proxy/migrations` to the `loco_obs` database, and `/readyz` fails until they succeed. Replicas take turns through a lock row in the `loco_migration_lock` KeeperMap table, stored in ClickHouse Keeper: each one claims the row while it migrates, renews its expiry every `MIGRATION_LOCK_RETRY_PERIOD` (2 seconds by default) until it finishes and deletes it after, so one migration runs at a time and a replica that finds the schema current applies nothing. A replica that dies holding the lock blocks the others until the row's `MIGRATION_LOCK_TTL` expiry (15 seconds by default) passes, and a holder that cannot renew stops migrating within that time. `MIGRATION_RETRY_BUDGET` (2 minutes by default) covers waiting for the lock and migrating together; a replica that runs out of it exits and restarts. The collectors only insert. Every table has `OrganizationId`, `WorkspaceId`, `EnvironmentId` and `ResourceId` columns, computed from the `loco.organization.id`, `loco.workspace.id`, `loco.environment.id` and `loco.resource.id` resource attributes, and its ordering key starts with `WorkspaceId`. The collectors set those attributes from the `loco.io/*-id` labels the controller puts on app and builder pods, and drop any value a client sends; Envoy's own telemetry takes `WorkspaceId` and `ResourceId` from the route instead and has no `OrganizationId` or `EnvironmentId`. Every collector also sets `loco.region` on every resource to the chart's `global.region`, replacing any value a client sends, so each row records the region of the cluster that collected it. `QueryMetrics` filters on `WorkspaceId` and the requested `ResourceId`s, and on `EnvironmentId` when the request sets `environment_id`, so a query that names an environment leaves out Envoy's metrics, and on `loco.region` when the request sets `region`. The dashboard's resource page names the active environment and region and sends queries through its configured control-plane API. That API forwards observability requests to its regional observability proxy. Rows expire after the proxy's `CLICKHOUSE_LOGS_TTL`, `CLICKHOUSE_TRACES_TTL` and `CLICKHOUSE_METRICS_TTL` durations, 365 days each by default; a migration applies them when it creates a table, so changing a duration later does not alter an existing table.

`QueryMetrics` reads scalar Gauge and Sum data, including counters such as `demo.payment.transactions`. Time filters apply to sample timestamps at nanosecond precision; returned point timestamps mark the start of each aggregation bucket. Each resource and time bucket aggregates the stored samples with `avg`, `sum`, `min`, `max`, `p50`, `p95` or `p99`, across the metric's attribute streams. Gauge results keep their existing shape. Sum series carry `metric_type: sum`, `aggregation_temporality` as the OTel enum number (`0` unspecified, `1` delta, `2` cumulative), and `is_monotonic: true` or `false`. Different sum temporalities and monotonicity flags produce separate series; a same-name gauge stays separate too. Clients must identify a series by its resource ID and labels.

These aggregations do not calculate rates or increases, convert delta and cumulative values, or compensate for counter resets. For cumulative samples `12`, `18`, `3`, `max` returns `18` and `sum` returns `33`, including the reset sample. Delta samples `2`, `4` produce a `sum` of `6`. Histograms, exponential histograms and summaries contain distributions and are outside this scalar endpoint. Queries retain workspace authorization and workspace, resource, environment, region, metric-name and time filters for both scalar tables.

## Deployment boundaries

[Deployment modes](../deployment/modes.md) determine the operator and tenancy of the platform. Application workspaces, environments, and regions determine where a team's workloads run within the installation.

For the protobuf service definitions and client generation, see [CLI and API reference](cli-api.md).
