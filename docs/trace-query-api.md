# Trace queries

`ObservabilityProxyService.ListTraces` returns one summary per trace for the traces list. `GetTrace` returns every stored span of one trace for the drawer. The regional control-plane API serves both RPCs from its ClickHouse; they share workspace/resource authorization and ClickHouse tenancy filtering.

Both requests require a `scope` with a workspace ID. Optional resource IDs and an environment ID narrow the visible spans. A caller needs workspace READ, or must name resources and have READ on every one. Scope IDs must be lowercase UUIDs; a resource from another workspace returns no rows.

## List traces

```json
{
  "scope": {"workspaceId": "11111111-2222-3333-4444-555555555555"},
  "startTime": "2026-10-14T12:00:00Z",
  "endTime": "2026-10-14T13:00:00Z",
  "limit": 25,
  "offset": 0,
  "status": "TRACE_STATUS_ERROR",
  "sort": "TRACE_SUMMARY_SORT_DURATION"
}
```

The inclusive time window selects span start times. The API enforces its configured maximum range and page limits. A zero limit uses the configured default. Duration minimum/maximum and status filters apply to the grouped trace. Every root-name term must occur in the root name, ignoring case. An optional trace ID selects a single trace. IDs must be 32 lowercase hexadecimal characters and nonzero.

Summaries contain trace ID, root span name, service, resource, start time, observed duration, status and distinct span count. Resource/time filtering can hide a root, so metadata then comes from the earliest visible span. Duration runs from the first visible start to the last visible end. An error in any visible span marks the trace as `TRACE_STATUS_ERROR`; other traces are `TRACE_STATUS_OK`. An unspecified status skips status filtering.

The response contains `summaries` and `totalCount`. Sorting defaults to start time descending; duration sorting and ascending order are available. Trace ID breaks ties. Paging applies after grouping, so a page cannot split a trace. Counts and pages reflect live ingestion rather than a pinned snapshot.

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

`mise run test:obs-proxy` exercises real ClickHouse grouping, filters, counts, ordering, page boundaries, whole-trace retrieval, ingestion and tenancy. `mise run test:api` covers both RPCs' validation, authorization and whole-trace reads through the API. `mise run e2e:observability` checks a known gateway-to-app trace through `GetTrace`, its one-row `ListTraces` summary and workspace isolation.
