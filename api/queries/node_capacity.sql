-- name: LockCapacityReporter :one
SELECT id FROM clusters
WHERE id = $1 AND sync_generation = $2 AND removed_at IS NULL FOR SHARE;

-- name: DeletePartialCapacity :exec
DELETE FROM node_capacity_reports WHERE cluster_id = $1 AND NOT complete;

-- name: BeginNodeCapacity :exec
INSERT INTO node_capacity_reports (id, cluster_id, generation, sequence, node_count, allocation_count)
VALUES ($1, $2, $3, -1, $4, $5);

-- name: AdvanceNodeCapacity :one
UPDATE node_capacity_reports SET sequence = sequence + 1
WHERE id = $1 AND cluster_id = $2 AND generation = $3
    AND sequence = sqlc.arg(previous_sequence) AND NOT complete
    AND node_count = sqlc.arg(node_count) AND allocation_count = sqlc.arg(allocation_count)
RETURNING id;

-- name: InsertNodeCapacity :copyfrom
INSERT INTO node_capacity (report_id, name, ready, unschedulable, cpu_allocatable, memory_allocatable, cpu_requested, memory_requested)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: InsertNodeAllocations :copyfrom
INSERT INTO node_allocations (report_id, node, workspace_id, resource_id, kind, cpu_millicores, memory_bytes, pods, environment_id, build_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ValidateNodeCapacity :one
SELECT
 (SELECT count(*) FROM node_capacity n WHERE n.report_id = r.id) = r.node_count
 AND (SELECT count(*) FROM node_allocations a WHERE a.report_id = r.id) = r.allocation_count
 AND NOT EXISTS (
   SELECT 1 FROM node_capacity n
   LEFT JOIN (SELECT node, sum(cpu_millicores)::bigint AS cpu, sum(memory_bytes)::bigint AS memory
              FROM node_allocations na WHERE na.report_id = $1 GROUP BY node) a ON a.node = n.name
   WHERE n.report_id = r.id
     AND (n.cpu_requested <> COALESCE(a.cpu, 0) OR n.memory_requested <> COALESCE(a.memory, 0))
 ) AS valid
FROM node_capacity_reports r WHERE r.id = $1;

-- name: DeleteCompleteCapacity :exec
DELETE FROM node_capacity_reports WHERE cluster_id = $1 AND complete;

-- name: PublishNodeCapacity :exec
UPDATE node_capacity_reports SET complete = true WHERE id = $1;

-- name: GetNodeCapacityReport :one
SELECT id, reported_at FROM node_capacity_reports WHERE cluster_id = $1 AND complete;

-- name: ListNodeCapacity :many
SELECT n.*, (SELECT count(DISTINCT a.resource_id)::int FROM node_allocations a
    WHERE a.report_id = n.report_id AND a.node = n.name AND a.resource_id <> '') AS resource_count,
    count(*) OVER ()::int AS total_count
FROM node_capacity n
WHERE n.report_id = $1 AND (sqlc.arg(query)::text = '' OR name ILIKE sqlc.arg(query))
ORDER BY CASE WHEN sqlc.arg(memory)::bool THEN memory_requested ELSE cpu_requested END DESC, name
LIMIT 10 OFFSET sqlc.arg(page_offset);

-- name: CountNodeResources :one
SELECT count(DISTINCT resource_id)::int FROM node_allocations WHERE report_id = $1 AND resource_id <> '';

-- name: GetNodeCapacity :one
SELECT n.*, (SELECT count(DISTINCT a.resource_id)::int FROM node_allocations a
    WHERE a.report_id = n.report_id AND a.node = n.name AND a.resource_id <> '') AS resource_count
FROM node_capacity n WHERE n.report_id = $1 AND name = $2;

-- name: GetNodeSystemAllocation :one
SELECT COALESCE(sum(cpu_millicores),0)::bigint AS cpu_millicores,
 COALESCE(sum(memory_bytes),0)::bigint AS memory_bytes, COALESCE(sum(pods),0)::int AS pods
FROM node_allocations WHERE report_id = $1 AND node = $2 AND kind = 'system';

-- name: ListNodeConsumers :many
WITH consumers AS (
 SELECT CASE WHEN sqlc.arg(workspace_id)::text = '' THEN a.workspace_id ELSE a.resource_id END::text AS id,
   COALESCE(CASE WHEN sqlc.arg(workspace_id)::text = '' THEN w.name ELSE r.name END,
            CASE WHEN sqlc.arg(workspace_id)::text = '' THEN a.workspace_id ELSE a.resource_id END)::text AS name,
   COALESCE(w.org_id::text, '')::text AS org_id, COALESCE(o.name, '')::text AS org_name,
   a.workspace_id,
   sum(a.cpu_millicores)::bigint AS cpu_millicores, sum(a.memory_bytes)::bigint AS memory_bytes,
   count(DISTINCT a.resource_id)::int AS resource_count, sum(a.pods)::int AS pods
 FROM node_allocations a
 LEFT JOIN workspaces w ON w.id = NULLIF(a.workspace_id,'')::uuid
 LEFT JOIN organizations o ON o.id = w.org_id
 LEFT JOIN resources r ON r.id = NULLIF(a.resource_id,'')::uuid
 WHERE a.report_id = $1 AND a.node = $2 AND a.kind <> 'system'
   AND (sqlc.arg(workspace_id)::text = '' OR a.workspace_id = sqlc.arg(workspace_id))
 GROUP BY 1,2,3,4,a.workspace_id
), ranked AS (
 SELECT *, row_number() OVER (ORDER BY CASE WHEN sqlc.arg(memory)::bool THEN memory_bytes ELSE cpu_millicores END DESC, id) AS rank
 FROM consumers
)
SELECT *, count(*) OVER ()::int AS total_count FROM ranked
WHERE (NOT sqlc.arg(remainder_only)::bool OR rank > 12)
  AND (sqlc.arg(query)::text = '' OR name ILIKE sqlc.arg(query) OR org_name ILIKE sqlc.arg(query))
ORDER BY rank LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(page_offset);

-- name: GetNodeRemainder :one
WITH consumers AS (
 SELECT CASE WHEN sqlc.arg(workspace_id)::text = '' THEN workspace_id ELSE resource_id END AS id,
 sum(cpu_millicores)::bigint AS cpu_millicores, sum(memory_bytes)::bigint AS memory_bytes,
 count(DISTINCT resource_id)::int AS resource_count, sum(pods)::int AS pods
 FROM node_allocations WHERE report_id = $1 AND node = $2 AND kind <> 'system'
 AND (sqlc.arg(workspace_id)::text = '' OR workspace_id = sqlc.arg(workspace_id))
 GROUP BY 1
), remainder AS (
 SELECT * FROM consumers ORDER BY CASE WHEN sqlc.arg(memory)::bool THEN memory_bytes ELSE cpu_millicores END DESC, id OFFSET 12
)
SELECT count(*)::int AS total_count, COALESCE(sum(cpu_millicores),0)::bigint AS cpu_millicores,
 COALESCE(sum(memory_bytes),0)::bigint AS memory_bytes, COALESCE(sum(resource_count),0)::int AS resource_count,
 COALESCE(sum(pods),0)::int AS pods FROM remainder;

-- name: ListWorkspaceAllocations :many
WITH coverage AS (
 SELECT count(r.id)::int AS reporting_clusters,
 count(*) FILTER (WHERE r.reported_at < now() - make_interval(secs => sqlc.arg(stale_seconds)::float8))::int AS stale_clusters,
 count(*) FILTER (WHERE r.id IS NULL)::int AS missing_clusters,
 COALESCE(min(r.reported_at), 'epoch'::timestamptz)::timestamptz AS oldest_reported_at
 FROM clusters c LEFT JOIN node_capacity_reports r ON r.cluster_id = c.id AND r.complete
 WHERE c.removed_at IS NULL
)
SELECT w.id, w.name, w.org_id,
 (SELECT count(*)::int FROM resources r WHERE r.workspace_id = w.id) AS resource_count,
 COALESCE(a.cpu_millicores,0)::bigint AS cpu_millicores, COALESCE(a.memory_bytes,0)::bigint AS memory_bytes,
 coverage.*, count(*) OVER ()::int AS total_count
FROM workspaces w CROSS JOIN coverage
LEFT JOIN LATERAL (
 SELECT sum(a.cpu_millicores)::bigint AS cpu_millicores, sum(a.memory_bytes)::bigint AS memory_bytes
 FROM node_allocations a JOIN node_capacity_reports r ON r.id = a.report_id
 JOIN clusters c ON c.id = r.cluster_id AND c.removed_at IS NULL
 WHERE a.workspace_id = w.id::text AND r.complete
) a ON true
WHERE w.org_id = $1 AND (sqlc.arg(query)::text = '' OR w.name ILIKE sqlc.arg(query))
ORDER BY
 CASE WHEN sqlc.arg(ascending)::bool THEN
   CASE WHEN sqlc.arg(memory)::bool THEN COALESCE(a.memory_bytes,0) ELSE COALESCE(a.cpu_millicores,0) END END ASC,
 CASE WHEN NOT sqlc.arg(ascending)::bool THEN
   CASE WHEN sqlc.arg(memory)::bool THEN COALESCE(a.memory_bytes,0) ELSE COALESCE(a.cpu_millicores,0) END END DESC,
 w.id
LIMIT 25 OFFSET sqlc.arg(page_offset);
