---
title: "Platform telemetry"
---

Platform telemetry is the logs, metrics and traces Loco's own components produce: the API, the controllers and the Kubernetes clusters they run on. It is off by default. Operators turn it on to watch the installation itself, across the control plane and every worker cluster.

## What is collected

| Source | Signals | Turned on by |
| --- | --- | --- |
| Control-plane gateway | Request traces linked to API spans, as `loco-gateway`; `gateway.tracing.samplingRate` defaults to 100 percent | `platformTelemetry.enabled` with the built-in system ingest |
| API | Traces of requests and RPCs, Go runtime metrics | `loco-control-plane` value `platformTelemetry.enabled` |
| Observability proxy | Workload ingestion traces, including the storage write and record counts | `loco-control-plane` value `platformTelemetry.enabled` |
| Control-plane `nodeCollector` | API and observability proxy container stdout logs; node, pod and container usage from the kubelet | `platformTelemetry.enabled`; logs can be disabled separately |
| Control-plane `clusterCollector` | Kubernetes cluster state, node capacity and conditions, and events | `platformTelemetry.enabled` |
| Application and build controllers | Traces of reconciles and Kubernetes API calls, controller-runtime metrics (reconciles, work queues, client requests), Go runtime metrics | `loco-worker` value `global.platformTelemetry.enabled` |
| Cluster node collectors | Platform container stdout logs, node metrics, cluster state and Kubernetes events from the Loco, collector and `global.platformTelemetry.namespaces` namespaces | `loco-worker` value `global.platformTelemetry.enabled` |

The API, controllers and observability proxy write structured logs to standard output. The `loco-worker` chart's collector DaemonSet tails container files on each node. With `global.platformTelemetry.enabled`, logs from allowed platform namespaces go to the system tenant, with Kubernetes identity, typed fields and trace/span correlation preserved. Workload namespaces use a separate log pipeline. Collector pods' own logs are excluded to prevent collection loops.

The control-plane chart installs `nodeCollector` and `clusterCollector` when `platformTelemetry.enabled` is true. The node collector runs once per node; the cluster collector runs one active instance for the whole cluster. `platformTelemetry.logs.enabled` defaults to true; setting it to false stops stdout forwarding while preserving kubelet metrics, cluster state and events. The API and proxy export their SDK telemetry directly to the configured endpoint.

The worker chart uses the same role names with its own configuration. `workloadCollector` receives application and Envoy OTLP telemetry; it is enabled independently of platform telemetry. The controllers send platform SDK telemetry to `clusterCollector`. `global.platformTelemetry.logs.enabled: false` disables only platform stdout forwarding; customer logs, metrics and traces remain enabled.

The proxy traces admitted, decoded batches that retain at least one non-system record after tenancy filtering. HTTP and gRPC use the same ingestion path. It starts a root span rather than adopting a span from the submitted telemetry; attributes report accepted and rejected record counts, and a storage failure marks the span as an error. Health checks, authentication failures and decoding failures do not produce proxy spans. Proxy logs remain on standard output; the setting does not enable a proxy OTLP log exporter or runtime metric exporter.

System-only batches produce no proxy spans, whether authenticated with the system ingest token or sent through a worker's platform pipelines. A mixed batch produces one ingestion span when it retains workload records. When that span returns to the proxy as system telemetry, ingestion stores it without generating another span. Loco's own SDK resources always carry `loco.telemetry.scope=platform`, even when operator resource attributes try to replace it. External collectors forwarding these signals back with a worker token must preserve that platform resource marker; arbitrary identity rewriting is outside this guarantee. With the marker preserved, self-ingestion suppresses the next generation of spans. The ClickHouse exporter's internal instrumentation and the proxy's authorization/export HTTP clients remain uninstrumented, so they cannot create another export cycle.

Ingestion failure diagnostics stay visible on proxy stdout with `loco.telemetry.internal=true`. Node collectors exclude those internal diagnostics from forwarding, so an unavailable backend cannot produce another generation of logs. Other proxy diagnostics remain collected.

The proxy stops its trace provider before stopping ingestion listeners so it can flush telemetry to itself. Requests that finish after the provider has stopped do not export a final ingestion span.

## Where it goes

By default, everything goes to the control plane's observability proxy, which stores it in ClickHouse under the **system tenant**: rows whose `WorkspaceId` is `00000000-0000-0000-0000-000000000001`. No workspace has that ID, so no customer query returns these rows. Each row names its sender in `ResourceAttributes['loco.cluster.id']`: the collecting cluster's ID for node-collected logs, or `control-plane` for directly exported API and proxy traces and metrics. `ServiceName` names the component, such as `loco-api`.

The proxy accepts system telemetry only from Loco's own senders:

- The API, proxy, gateway collector and control-plane node and cluster collectors present a token the `loco-control-plane` chart generates and keeps in the Secret `<release>-system-ingest-token`.
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

- Queries accept `@component` (the sending component, such as `loco-api` or `loco-controller`), `@cluster` (the worker cluster's ID, or `control-plane`) and `@node` (the Kubernetes node name), next to `@level`, `@region`, `@replica`, text search and the other [query syntax](../reference/log-queries.md). In metrics requests, `attributes` accepts the keys `component`, `cluster` and `node`. The `node` key matches `ResourceAttributes['k8s.node.name']`, together with `cluster` to distinguish equal node names in different clusters.
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

On the control plane, set its physical region. This is independent of the default region for new services:

```yaml
platformTelemetry:
  enabled: true
  region: us-ca-1
```

`platformTelemetry.collectionInterval` defaults to 60s for kubelet and cluster metrics. Events use a watch. The node collector measures actual resource usage; the cluster collector reads Kubernetes object state, such as replicas, allocatable capacity and node conditions. API runtime metrics describe the API process and do not replace these infrastructure metrics.

Each control-plane role follows `platformTelemetry.enabled` by default. Setting `nodeCollector.enabled: false` or `clusterCollector.enabled: false` delegates that role while retaining SDK export. When both installation charts share a cluster, let the worker own both collection roles and disable the control-plane roles. Add the control-plane namespace to the worker's `global.platformTelemetry.namespaces` when necessary. Running two owners would duplicate node metrics and cluster state. Collector deployment settings, including `namespaceOverride`, belong under the role values.

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

Control-plane gateway tracing uses the built-in system ingest. An external gateway tracing destination needs a separate OTLP/gRPC configuration.

Each chart takes an OTLP/HTTP endpoint and headers for an operator's own collector or backend. With an endpoint set, that chart's platform telemetry goes only there and nothing of it reaches the observability proxy; customer telemetry still does.

```yaml
# loco-control-plane
platformTelemetry:
  enabled: true
  region: us-ca-1
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

The API exports traces to `<endpoint>/v1/traces` and metrics to `/v1/metrics`. Controllers send traces and metrics to the cluster collector. Control-plane collectors use `platformTelemetry.exporter`; worker platform collectors use `global.platformTelemetry.exporter`. Collectors forward their records with persistent queues and retries. See the [`loco-control-plane`](https://github.com/team-loco/loco/blob/main/charts/loco-control-plane/README.md#platform-telemetry) and [`loco-worker`](https://github.com/team-loco/loco/blob/main/charts/loco-worker/README.md#kubernetes-platform-telemetry) chart READMEs for the details.
