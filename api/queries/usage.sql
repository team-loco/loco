-- name: UpsertWorkloadRuns :exec
WITH reported AS (
    SELECT unnest(sqlc.arg(pod_uids)::uuid[]) AS pod_uid,
           unnest(sqlc.arg(organization_ids)::uuid[]) AS organization_id,
           unnest(sqlc.arg(workspace_ids)::uuid[]) AS workspace_id,
           unnest(sqlc.arg(resource_ids)::uuid[]) AS resource_id,
           unnest(sqlc.arg(environment_ids)::uuid[]) AS environment_id,
           unnest(sqlc.arg(cpu_millicores)::bigint[]) AS cpu_millicores,
           unnest(sqlc.arg(memory_bytes)::bigint[]) AS memory_bytes,
           unnest(sqlc.arg(started_ats)::timestamptz[]) AS started_at,
           unnest(sqlc.arg(ended)::boolean[]) AS ended,
           unnest(sqlc.arg(ended_ats)::timestamptz[]) AS ended_at
)
INSERT INTO workload_runs (
    pod_uid, cluster_id, region, organization_id, workspace_id, resource_id, environment_id,
    cpu_millicores, memory_bytes, started_at, last_seen_at, ended_at
)
SELECT r.pod_uid, c.id, c.region, r.organization_id, r.workspace_id, r.resource_id, r.environment_id,
       r.cpu_millicores, r.memory_bytes, r.started_at,
       GREATEST(r.started_at, CASE WHEN r.ended THEN r.ended_at ELSE sqlc.arg(observed_at)::timestamptz END),
       CASE WHEN r.ended THEN GREATEST(r.started_at, r.ended_at) END
FROM reported r
JOIN clusters c ON c.id = sqlc.arg(cluster_id)
ON CONFLICT (pod_uid) DO UPDATE
SET started_at = LEAST(workload_runs.started_at, EXCLUDED.started_at),
    last_seen_at = COALESCE(
        workload_runs.ended_at, EXCLUDED.ended_at, GREATEST(workload_runs.last_seen_at, EXCLUDED.last_seen_at)
    ),
    ended_at = COALESCE(workload_runs.ended_at, EXCLUDED.ended_at),
    published = FALSE
WHERE workload_runs.cluster_id = EXCLUDED.cluster_id
  AND (
      LEAST(workload_runs.started_at, EXCLUDED.started_at),
      COALESCE(
          workload_runs.ended_at, EXCLUDED.ended_at, GREATEST(workload_runs.last_seen_at, EXCLUDED.last_seen_at)
      ),
      COALESCE(workload_runs.ended_at, EXCLUDED.ended_at)
  ) IS DISTINCT FROM (workload_runs.started_at, workload_runs.last_seen_at, workload_runs.ended_at);

-- name: RecordClusterUsageReport :exec
INSERT INTO cluster_usage_reports (cluster_id, feed, reported_at)
VALUES (sqlc.arg(cluster_id), sqlc.arg(feed), sqlc.arg(reported_at))
ON CONFLICT (cluster_id, feed, reported_at) DO NOTHING;

-- name: ListClusterUsageReportTimes :many
SELECT DISTINCT ON (c.id) c.id, r.reported_at
FROM clusters c
LEFT JOIN cluster_usage_reports r
    ON r.cluster_id = c.id AND r.feed = sqlc.arg(feed) AND r.published
WHERE c.removed_at IS NULL AND c.agent_public_key IS NOT NULL
ORDER BY c.id, r.reported_at DESC NULLS LAST;

-- name: ListWorkloadRunsAfter :many
SELECT *
FROM workload_runs
WHERE stream_position > sqlc.arg(after_position)::bigint
ORDER BY stream_position
LIMIT sqlc.arg(max_rows);

-- name: LockEgressCounters :many
SELECT gateway_pod_uid, route, bytes, observed_at
FROM egress_counters
WHERE cluster_id = sqlc.arg(cluster_id) AND gateway_pod_uid = ANY(sqlc.arg(gateway_pod_uids)::uuid[])
FOR UPDATE;

-- name: UpsertEgressCounters :exec
WITH reported AS (
    SELECT unnest(sqlc.arg(gateway_pod_uids)::uuid[]) AS gateway_pod_uid,
           unnest(sqlc.arg(routes)::text[]) AS route,
           unnest(sqlc.arg(bytes)::bigint[]) AS bytes
)
INSERT INTO egress_counters (gateway_pod_uid, route, cluster_id, bytes, observed_at)
SELECT r.gateway_pod_uid, r.route, sqlc.arg(cluster_id), r.bytes, sqlc.arg(observed_at)
FROM reported r
ON CONFLICT (gateway_pod_uid, route) DO UPDATE
SET bytes = EXCLUDED.bytes, observed_at = EXCLUDED.observed_at
WHERE egress_counters.cluster_id = EXCLUDED.cluster_id
  AND egress_counters.observed_at < EXCLUDED.observed_at;

-- name: DeleteStaleEgressCounters :exec
DELETE FROM egress_counters
WHERE cluster_id = sqlc.arg(cluster_id) AND observed_at < sqlc.arg(observed_at);

-- name: AddEgressUsage :exec
WITH reported AS (
    SELECT unnest(sqlc.arg(organization_ids)::uuid[]) AS organization_id,
           unnest(sqlc.arg(workspace_ids)::uuid[]) AS workspace_id,
           unnest(sqlc.arg(resource_ids)::uuid[]) AS resource_id,
           unnest(sqlc.arg(environment_ids)::uuid[]) AS environment_id,
           unnest(sqlc.arg(bytes)::bigint[]) AS bytes
), added AS (
    SELECT organization_id, workspace_id, resource_id, environment_id, sum(bytes)::bigint AS bytes
    FROM reported
    GROUP BY organization_id, workspace_id, resource_id, environment_id
)
INSERT INTO egress_usage (
    cluster_id, region, organization_id, workspace_id, resource_id, environment_id, hour_start, bytes
)
SELECT c.id, c.region, a.organization_id, a.workspace_id, a.resource_id, a.environment_id,
       sqlc.arg(hour_start)::timestamptz, a.bytes
FROM added a
JOIN clusters c ON c.id = sqlc.arg(cluster_id)
ON CONFLICT (cluster_id, resource_id, environment_id, hour_start) DO UPDATE
SET bytes = egress_usage.bytes + EXCLUDED.bytes,
    published = FALSE;

-- name: ListEgressUsageAfter :many
SELECT *
FROM egress_usage
WHERE stream_position > sqlc.arg(after_position)::bigint
ORDER BY stream_position
LIMIT sqlc.arg(max_rows);

-- name: ShareUsageStreamCounter :exec
SELECT position FROM usage_stream_counter WHERE singleton FOR SHARE;

-- name: LockUsageStreamCounter :one
SELECT position FROM usage_stream_counter WHERE singleton FOR UPDATE;

-- name: PublishClusterUsageReports :exec
UPDATE cluster_usage_reports SET published = TRUE WHERE NOT published;

-- name: PruneClusterUsageReports :exec
DELETE FROM cluster_usage_reports stale
USING cluster_usage_reports latest
WHERE stale.cluster_id = latest.cluster_id AND stale.feed = latest.feed
  AND stale.reported_at < latest.reported_at AND latest.published;

-- name: PublishWorkloadRuns :one
WITH numbered AS (
    SELECT pod_uid, sqlc.arg(position)::bigint + row_number() OVER (ORDER BY pod_uid) AS position
    FROM workload_runs WHERE NOT published
), published AS (
    UPDATE workload_runs SET stream_position = numbered.position, published = TRUE
    FROM numbered WHERE workload_runs.pod_uid = numbered.pod_uid
    RETURNING workload_runs.stream_position
)
SELECT COALESCE(max(stream_position), sqlc.arg(position))::bigint FROM published;

-- name: PublishEgressUsage :one
WITH numbered AS (
    SELECT id, sqlc.arg(position)::bigint + row_number() OVER (ORDER BY id) AS position
    FROM egress_usage WHERE NOT published
), published AS (
    UPDATE egress_usage SET stream_position = numbered.position, published = TRUE
    FROM numbered WHERE egress_usage.id = numbered.id
    RETURNING egress_usage.stream_position
)
SELECT COALESCE(max(stream_position), sqlc.arg(position))::bigint FROM published;

-- name: UpdateUsageStreamCounter :exec
UPDATE usage_stream_counter SET position = sqlc.arg(position) WHERE singleton;
