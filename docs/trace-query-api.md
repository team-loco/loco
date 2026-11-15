# Trace queries

`ObservabilityService.ListTraces` returns one summary per trace for the traces list. `QueryTraceBuckets` counts the same traces per time bucket for the traces overview charts. `GetTrace` returns every stored span of one trace for the drawer. The regional control-plane API serves these RPCs from its ClickHouse; they share workspace/resource authorization and ClickHouse tenancy filtering.

Every request requires a `scope` with a workspace ID. Optional resource IDs and an environment ID narrow the visible spans. A caller needs workspace READ, or must name resources and have READ on every one. Scope IDs must be lowercase UUIDs; a resource from another workspace returns no rows.

## List traces

```json
{
  "scope": {"workspaceId": "11111111-2222-3333-4444-555555555555"},
  "startTime": "2026-10-14T12:00:00Z",
  "endTime": "2026-10-14T13:00:00Z",
  "limit": 25,
  "offset": 0,
  "query": "@status:error @http.route:/api/*",
  "sort": "TRACE_SUMMARY_SORT_DURATION"
}
```

The inclusive time window selects span start times. The API enforces its configured maximum range and page limits. A zero limit uses the configured default. `query` is a [trace query](content/reference/log-queries.md#trace-queries): its text matches the root name ignoring case, `@status` and `@duration` apply to the grouped trace, and `@resource` and attribute fields match a trace when any of its visible spans matches. An empty query matches every trace. A query the API cannot parse fails with `INVALID_ARGUMENT`, a `BadRequest` violation on `query` and an `ErrorInfo` with reason `QUERY_SYNTAX` and the 1-based character `position`.

Summaries contain trace ID, root span name, service, resource, start time, observed duration, status and distinct span count. Resource/time filtering can hide a root, so metadata then comes from the earliest visible span. Duration runs from the first visible start to the last visible end. An error in any visible span marks the trace as `TRACE_STATUS_ERROR`; other traces are `TRACE_STATUS_OK`.

The response contains `summaries` and `totalCount`. Sorting defaults to start time descending; duration sorting and ascending order are available. Trace ID breaks ties. Paging applies after grouping, so a page cannot split a trace. Counts and pages reflect live ingestion rather than a pinned snapshot.

## Trace buckets

```json
{
  "scope": {"workspaceId": "11111111-2222-3333-4444-555555555555"},
  "startTime": "2026-10-14T12:00:00Z",
  "endTime": "2026-10-14T13:00:00Z",
  "query": "@status:error",
  "intervalSeconds": 60
}
```

`QueryTraceBuckets` takes the time window and `query` of `ListTraces`, and groups the traces it would list by the `intervalSeconds` bucket their start falls in. Buckets start at multiples of the interval since the Unix epoch. The response holds every bucket from the one containing `startTime` to the one containing `endTime`, in time order; each carries its start time, trace count, error count and the p50 and p95 trace durations, all zero for a bucket without traces. `total` carries the same counts and percentiles over the whole range. Percentiles interpolate between durations, and ClickHouse samples them once a bucket holds more than 8192 traces. A range spanning more than `LOCO_OBSERVABILITY_MAX_BUCKETS` (1440) buckets is rejected as `INVALID_ARGUMENT`.

## Get a trace

```json
{
  "scope": {"workspaceId": "11111111-2222-3333-4444-555555555555"},
  "traceId": "0123456789abcdef0123456789abcdef"
}
```

The response contains `spans` ordered by start time and span ID. It has no cursor, page limit or time-window filter. Spans carry parent IDs, service, timestamps, duration, status, scope identity, attributes and events. Missing parents do not authorize reads outside the requested scope. All stored spans can still be fewer than the application eventually exports; refresh to see later arrivals.

Nanosecond durations and counts use protobuf JSON decimal strings. Clients preserve them as strings or bigints. Queries use the existing connection pool and execution timeout, and responses retain the RPC message-size limit.

## Validation

`mise run test:obs-proxy` exercises real ClickHouse grouping, filters, counts, ordering, page boundaries, whole-trace retrieval, ingestion and tenancy. `mise run test:api` covers the RPCs' validation, authorization, bucket limit and whole-trace reads through the API. `mise run e2e:observability` checks a known gateway-to-app trace through `GetTrace`, its one-row `ListTraces` summary, its `QueryTraceBuckets` count and workspace isolation.
