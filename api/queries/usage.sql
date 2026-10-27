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
    xid = pg_current_xact_id()
WHERE workload_runs.cluster_id = EXCLUDED.cluster_id
  AND (
      LEAST(workload_runs.started_at, EXCLUDED.started_at),
      COALESCE(
          workload_runs.ended_at, EXCLUDED.ended_at, GREATEST(workload_runs.last_seen_at, EXCLUDED.last_seen_at)
      ),
      COALESCE(workload_runs.ended_at, EXCLUDED.ended_at)
  ) IS DISTINCT FROM (workload_runs.started_at, workload_runs.last_seen_at, workload_runs.ended_at);

-- name: PruneClusterUsageReports :exec
DELETE FROM cluster_usage_reports stale
WHERE stale.cluster_id = sqlc.arg(cluster_id)
  AND stale.reported_at < (
      SELECT max(final.reported_at) FROM cluster_usage_reports final
      WHERE final.cluster_id = sqlc.arg(cluster_id)
        AND final.xid < pg_snapshot_xmin(pg_current_snapshot())
  );

-- name: RecordClusterUsageReport :exec
INSERT INTO cluster_usage_reports (cluster_id, reported_at)
VALUES (sqlc.arg(cluster_id), sqlc.arg(reported_at))
ON CONFLICT (cluster_id, reported_at) DO NOTHING;

-- name: ListClusterUsageReportTimes :many
SELECT DISTINCT ON (c.id) c.id, r.reported_at
FROM clusters c
LEFT JOIN cluster_usage_reports r
    ON r.cluster_id = c.id AND r.xid < pg_snapshot_xmin(pg_current_snapshot())
WHERE c.removed_at IS NULL AND c.agent_public_key IS NOT NULL
ORDER BY c.id, r.reported_at DESC NULLS LAST;

-- name: ListWorkloadRunsAfter :many
SELECT *
FROM workload_runs
WHERE (xid, pod_uid) > (sqlc.arg(after_xid)::xid8, sqlc.arg(after_id)::uuid)
  AND xid < pg_snapshot_xmin(pg_current_snapshot())
ORDER BY xid, pod_uid
LIMIT sqlc.arg(max_rows);
