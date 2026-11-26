---
title: "Platform telemetry"
---

Platform telemetry is the logs, metrics and traces Loco's own components produce: the API, the controllers and the Kubernetes clusters they run on. It is off by default. Operators turn it on to watch the installation itself, across the control plane and every worker cluster.

## What is collected

| Source | Signals | Turned on by |
| --- | --- | --- |
| API | Traces of requests and RPCs, Go runtime metrics, every log record at `LOG_LEVEL` or above | `loco-control-plane` value `platformTelemetry.enabled` |
| Observability proxy | Workload ingestion traces, including the storage write and record counts | `loco-control-plane` value `platformTelemetry.enabled` |
| Application and build controllers | Traces of reconciles and Kubernetes API calls, controller-runtime metrics (reconciles, work queues, client requests), Go runtime metrics | `loco-worker` value `global.platformTelemetry.enabled` |
| Worker cluster | Node metrics, cluster state and Kubernetes events from the Loco, collector and `global.platformTelemetry.namespaces` namespaces | `loco-worker` value `global.platformTelemetry.enabled` |

The API keeps writing its logs to standard output either way. Customer workloads' telemetry is never part of it.

The proxy traces admitted, decoded batches that retain at least one non-system record after tenancy filtering. HTTP and gRPC use the same ingestion path. It starts a root span rather than adopting a span from the submitted telemetry; attributes report accepted and rejected record counts, and a storage failure marks the span as an error. Health checks, authentication failures and decoding failures do not produce proxy spans. Proxy logs remain on standard output; the setting does not enable a proxy OTLP log exporter or runtime metric exporter.

System-only batches produce no proxy spans, whether authenticated with the system ingest token or sent through a worker's platform pipelines. A mixed batch produces one ingestion span when it retains workload records. When that span returns to the proxy as system telemetry, ingestion stores it without generating another span. Loco's own SDK resources always carry `loco.telemetry.scope=platform`, even when operator resource attributes try to replace it. External collectors forwarding these signals back with a worker token must preserve that platform resource marker; arbitrary identity rewriting is outside this guarantee. With the marker preserved, self-ingestion suppresses the next generation of spans. The ClickHouse exporter's internal instrumentation and the proxy's authorization/export HTTP clients remain uninstrumented, so they cannot create another export cycle.

The proxy stops its trace provider before stopping ingestion listeners so it can flush telemetry to itself. Requests that finish after the provider has stopped do not export a final ingestion span.

## Where it goes

By default, everything goes to the control plane's observability proxy, which stores it in ClickHouse under the **system tenant**: rows whose `WorkspaceId` is `00000000-0000-0000-0000-000000000001`. No workspace has that ID, so no customer query returns these rows. Each row names its sender in `ResourceAttributes['loco.cluster.id']`: the worker cluster's ID, or `control-plane` for the API. `ServiceName` names the component, such as `loco-api`.

The proxy accepts system telemetry only from Loco's own senders:

- The API presents a token the `loco-control-plane` chart generates and keeps in the Secret `<release>-system-ingest-token`.
- A worker's collectors present the cluster's ingest token, as they do for application telemetry, and mark platform records with `loco.telemetry.scope=platform`. The collectors delete that marker from anything workloads send, and the proxy rejects a platform record that names a workspace.

System rows keep 30 days of every signal. Set `SYSTEM_RETENTION_DAYS` through `obsProxy.ingest.env` to change it:

```yaml
obsProxy:
  ingest:
    env:
      SYSTEM_RETENTION_DAYS: "14"
```

## Read it through the API

A user with the system read or system admin role reads the system tenant through the observability API, the same calls the dashboard uses for a workspace: logs, log buckets, live tail, metrics, traces, trace buckets and a single trace. A request asks for the installation scope by setting `installation: true` (`scope.installation` for traces) instead of `workspace_id`; it sets exactly one of the two. The system tenant's ID is not a workspace ID. Anyone else, including a workspace or organization admin, gets `permission_denied`. Grant the role with `locoadm admin`.

In the installation scope:

- Queries accept `@component` (the sending component, such as `loco-api` or `loco-controller`) and `@cluster` (the worker cluster's ID, or `control-plane`), next to `@level`, `@region`, `@replica`, text search and the other [query syntax](../reference/log-queries.md). In metrics requests, `attributes` accepts the keys `component` and `cluster`.
- `resource_ids` and `environment_id` belong to workspaces. A request that sets either, or a query that uses `@resource` or `@build`, fails with `invalid_argument`.
- Metrics cover what the senders export: the API's runtime and stream metrics, the controllers' runtime and controller-runtime metrics, and the collectors' platform metrics.

To read the rows directly, query ClickHouse as an admin:

```sql
SELECT Timestamp, ServiceName, SeverityText, Body
FROM loco_obs.otel_logs
WHERE WorkspaceId = '00000000-0000-0000-0000-000000000001'
ORDER BY Timestamp DESC
LIMIT 100
```

## Turn it on

On the control plane:

```yaml
platformTelemetry:
  enabled: true
```

On each worker, with the identity every platform record carries:

```yaml
global:
  platformTelemetry:
    enabled: true
    installation: production
    cluster: us-ca-1-a
    environment: production
```

## Send it somewhere else

Each chart takes an OTLP/HTTP endpoint and headers for an operator's own collector or backend. With an endpoint set, that chart's platform telemetry goes only there and nothing of it reaches the observability proxy; customer telemetry still does.

```yaml
# loco-control-plane
platformTelemetry:
  enabled: true
  exporter:
    endpoint: https://otel.example.com
    headers:
      Authorization: Bearer EXAMPLE_TOKEN
```

```yaml
# loco-worker
global:
  platformTelemetry:
    enabled: true
    installation: production
    cluster: us-ca-1-a
    environment: production
    exporter:
      endpoint: https://otel.example.com
      headers:
        Authorization: Bearer EXAMPLE_TOKEN
```

The API exports to `<endpoint>/v1/traces`, `/v1/metrics` and `/v1/logs`. On a worker, the controllers send to the cluster collector, which forwards platform records to the endpoint with its persistent queue and retries. See the [`loco-control-plane`](https://github.com/team-loco/loco/blob/main/charts/loco-control-plane/README.md#platform-telemetry) and [`loco-worker`](https://github.com/team-loco/loco/blob/main/charts/loco-worker/README.md#kubernetes-platform-telemetry) chart READMEs for the details.
