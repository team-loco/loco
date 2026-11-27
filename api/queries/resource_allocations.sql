-- name: ListResourceReservations :many
WITH matched AS MATERIALIZED (
 SELECT r.id, r.name FROM resources r
 WHERE r.workspace_id = sqlc.arg(workspace_id)::uuid
 AND (sqlc.arg(all_resources)::bool OR r.id = ANY(sqlc.arg(resource_ids)::uuid[]))
 AND (sqlc.arg(query)::text = '' OR r.name ILIKE sqlc.arg(query) OR r.slug ILIKE sqlc.arg(query))
), observed AS MATERIALIZED (
 SELECT a.*, nr.cluster_id, b.origin_environment_id,
   (sqlc.arg(environment_id)::text = '' OR
    CASE WHEN a.kind = 'build' THEN b.origin_environment_id::text ELSE a.environment_id END = sqlc.arg(environment_id)) AS in_scope
 FROM node_allocations a
 JOIN node_capacity_reports nr ON nr.id = a.report_id AND nr.complete
 JOIN clusters c ON c.id = nr.cluster_id AND c.removed_at IS NULL
 JOIN matched m ON m.id = NULLIF(a.resource_id,'')::uuid
 LEFT JOIN builds b ON b.id = NULLIF(a.build_id,'')::uuid
 WHERE a.workspace_id = sqlc.arg(workspace_id)::uuid::text
), totals AS (
 SELECT resource_id,
 COALESCE(sum(cpu_millicores) FILTER (WHERE in_scope),0)::bigint AS cpu,
 COALESCE(sum(memory_bytes) FILTER (WHERE in_scope),0)::bigint AS memory,
 COALESCE(sum(cpu_millicores) FILTER (WHERE in_scope AND kind = 'build'),0)::bigint AS build_cpu,
 COALESCE(sum(memory_bytes) FILTER (WHERE in_scope AND kind = 'build'),0)::bigint AS build_memory,
 COALESCE(sum(cpu_millicores) FILTER (WHERE kind = 'build' AND origin_environment_id IS NULL),0)::bigint AS unscoped_build_cpu,
 COALESCE(sum(memory_bytes) FILTER (WHERE kind = 'build' AND origin_environment_id IS NULL),0)::bigint AS unscoped_build_memory
 FROM observed GROUP BY resource_id
), page AS (
 SELECT m.id,m.name,COALESCE(t.cpu,0)::bigint AS cpu,COALESCE(t.memory,0)::bigint AS memory,
 COALESCE(t.build_cpu,0)::bigint AS build_cpu,COALESCE(t.build_memory,0)::bigint AS build_memory,
 COALESCE(t.unscoped_build_cpu,0)::bigint AS unscoped_build_cpu,
 COALESCE(t.unscoped_build_memory,0)::bigint AS unscoped_build_memory,
 count(*) OVER ()::int AS total_count
 FROM matched m LEFT JOIN totals t ON t.resource_id = m.id::text
 ORDER BY
 CASE WHEN sqlc.arg(ascending)::bool THEN CASE WHEN sqlc.arg(memory)::bool THEN COALESCE(t.memory,0) ELSE COALESCE(t.cpu,0) END END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::bool THEN CASE WHEN sqlc.arg(memory)::bool THEN COALESCE(t.memory,0) ELSE COALESCE(t.cpu,0) END END DESC,m.id
 LIMIT 25 OFFSET sqlc.arg(page_offset)
), targets AS (
 SELECT p.resource_id,p.cluster_id FROM placements p JOIN page m ON m.id = p.resource_id
 WHERE NOT p.desired_deleted AND (sqlc.arg(environment_id)::text = '' OR p.environment_id::text = sqlc.arg(environment_id))
 UNION
 SELECT b.resource_id,b.cluster_id FROM builds b JOIN page m ON m.id = b.resource_id
 WHERE b.cluster_id IS NOT NULL AND b.status IN ('queued','running')
 AND (sqlc.arg(environment_id)::text = '' OR b.origin_environment_id::text = sqlc.arg(environment_id) OR b.origin_environment_id IS NULL)
 UNION
 SELECT o.resource_id::uuid,o.cluster_id FROM observed o JOIN page m ON m.id=o.resource_id::uuid WHERE o.in_scope OR (o.kind = 'build' AND o.origin_environment_id IS NULL)
), coverage AS (
 SELECT t.resource_id,count(nr.id)::int AS reporting_clusters,
 count(*) FILTER (WHERE nr.reported_at < now()-make_interval(secs=>sqlc.arg(stale_seconds)::float8))::int AS stale_clusters,
 count(*) FILTER (WHERE nr.id IS NULL)::int AS missing_clusters,
 COALESCE(min(nr.reported_at),'epoch'::timestamptz)::timestamptz AS oldest_reported_at
 FROM targets t JOIN clusters c ON c.id=t.cluster_id AND c.removed_at IS NULL
 LEFT JOIN node_capacity_reports nr ON nr.cluster_id=c.id AND nr.complete
 GROUP BY t.resource_id
)
SELECT page.*, COALESCE(coverage.reporting_clusters,0)::int AS reporting_clusters,
COALESCE(coverage.stale_clusters,0)::int AS stale_clusters,
COALESCE(coverage.missing_clusters,0)::int AS missing_clusters,
COALESCE(coverage.oldest_reported_at,'epoch'::timestamptz)::timestamptz AS oldest_reported_at
FROM page LEFT JOIN coverage ON coverage.resource_id=page.id

ORDER BY
 CASE WHEN sqlc.arg(ascending)::bool THEN CASE WHEN sqlc.arg(memory)::bool THEN page.memory ELSE page.cpu END END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::bool THEN CASE WHEN sqlc.arg(memory)::bool THEN page.memory ELSE page.cpu END END DESC,page.id;
