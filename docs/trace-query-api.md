# Trace query API

`loco.observability.v1.ObservabilityProxyService/QueryTraces` queries spans through the selected regional control-plane address. It uses the same bearer credential and workspace READ permission as QueryLogs and QueryMetrics. The control-plane ingress forwards the RPC to its separate observability service, which reads that region's ClickHouse. One selected regional address receives one request, independent of worker-cluster count or availability. There is no client fanout or cross-region replication. The query address, private ClickHouse DSNs, and collector ingestion endpoint are separate configuration.

```json
{
  "workspaceId": "11111111-2222-3333-4444-555555555555",
  "startTime": "2026-10-14T12:00:00Z",
  "endTime": "2026-10-14T13:00:00Z",
  "traceId": "0123456789abcdef0123456789abcdef",
  "limit": 1000
}
```

The workspace and both timestamps are required. The inclusive time range selects span **start times**, not intervals overlapping the window. The maximum range and default/maximum page size use the existing proxy guardrails. The request's limit must be between zero and 10000; zero uses the configured default and positive limits are clamped to the configured maximum. Queries use the existing ClickHouse connection pool and execution timeout. That pool limits concurrent database connections globally; the current proxy does not enforce separate per-workspace concurrency quotas for any query RPC.

An omitted trace ID finds spans across traces. A supplied trace ID must be exactly 32 lowercase hexadecimal characters and nonzero. Optional `resourceIds` and `environmentId` narrow the returned spans; they never expand the workspace boundary. Workspace, environment and resource IDs must be UUIDs. Timestamp values must be valid protobuf timestamps representable at signed 64-bit nanosecond precision.

The response contains `spans` and `nextCursor`. Spans include trace/span/parent IDs, operation name, kind, service name, start timestamp, duration, status, workspace/environment/resource IDs, resource and span attributes, and instrumentation scope. The proxy's embedded [ClickHouse exporter](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/v0.163.0/exporter/clickhouseexporter/exporter_traces.go#L105) writes the difference between end/start timestamps as nanoseconds. `durationNanos` is that unsigned 64-bit nanosecond count, emitted as a **decimal string** by protobuf JSON; clients must preserve it as a string or bigint rather than converting to a JavaScript number.

Results are ordered by start timestamp ascending, with persisted ClickHouse block number and block offset breaking ties. The cursor has the same position format as log pagination and remains usable across table merges. Send the response cursor with the original filters and time bounds to fetch the next page. An empty cursor means that query had no further rows at execution time. A page is not a whole trace: keep paging before checking connectivity. Concurrent arrivals are not a snapshot; a span arriving with a start time before an already consumed cursor may require a fresh query from the first page. Poll from the first page while waiting for an asynchronously exported trace to become complete.

Resource/environment filters and time bounds can omit a span's parent, and upstream spans may belong to another workspace. Parent IDs are returned unchanged; missing parents do not authorize access outside the requested workspace or filters. Completeness is determined by the application and the expected spans, not by the absence of a next cursor alone. The response intentionally returns stored status/kind strings and scope identity without inventing missing data.

`mise run test:obs-proxy -p 1` exercises migrations, real ClickHouse reads, tenancy, pagination across merges, nanosecond precision, and HTTP authorization/validation. `mise run e2e:observability` also validates a known gateway-to-app trace through this API with one span per page and checks a second workspace's isolation. Its setup creates a kind cluster; the existing suite still uses direct SQL for independent collector/schema diagnostics, but trace connectivity and trace access checks use QueryTraces.

The trace table migration enables persisted block numbers and offsets, matching the log table, so identical timestamp/span records remain independently pageable after merges. Under the repository's migration policy this edits the existing migration in place. A database that already applied the previous trace migration must have those settings enabled or be recreated before using this API; this change does not supply an upgrade migration for an existing installation.
