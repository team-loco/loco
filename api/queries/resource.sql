-- Resource queries

-- name: CreateResource :one
INSERT INTO resources (workspace_id, slug, name, type, description, spec, spec_version, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: GetResourceByID :one
SELECT r.id, r.workspace_id, r.slug, r.name, r.type, r.description, r.spec, r.spec_version, r.revision, r.partial, r.restart_policy, r.max_restarts, r.deploy_timeout_seconds, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.id = $1;

-- name: GetResourceBySlugAndWorkspace :one
SELECT r.id, r.workspace_id, r.slug, r.name, r.type, r.description, r.spec, r.spec_version, r.revision, r.partial, r.restart_policy, r.max_restarts, r.deploy_timeout_seconds, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1 AND r.slug = $2;

-- name: ListResourcesByIDs :many
SELECT r.id, r.workspace_id, r.slug, r.name, r.type, r.description, r.spec, r.spec_version, r.revision, r.partial, r.restart_policy, r.max_restarts, r.deploy_timeout_seconds, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.id = ANY(sqlc.arg(ids)::uuid[]);

-- name: UpdateResource :one
UPDATE resources
SET name = COALESCE(sqlc.narg('name'), name),
    slug = COALESCE(sqlc.narg('slug'), slug),
    description = COALESCE(sqlc.narg('description'), description),
    updated_at = NOW()
WHERE id = $1
RETURNING id;

-- name: UpdateResourceSpec :exec
UPDATE resources
SET spec = $2, revision = revision + 1, updated_at = NOW()
WHERE id = $1;

-- name: SetResourceDeployPolicy :exec
UPDATE resources
SET restart_policy = sqlc.narg('restart_policy'),
    max_restarts = sqlc.narg('max_restarts'),
    deploy_timeout_seconds = sqlc.narg('deploy_timeout_seconds'),
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteResource :exec
DELETE FROM resources WHERE id = $1;

-- name: SetResourcePartial :exec
UPDATE resources
SET partial = $2, updated_at = NOW()
WHERE id = $1;

-- name: LockResources :many
SELECT id FROM resources
WHERE id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY id
FOR UPDATE;

-- name: GetResourceWorkspaceID :one
SELECT workspace_id FROM resources WHERE id = $1;

-- name: ListEligibleClusters :many
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status,
       last_health_check, last_heartbeat, capacity_cpu_millicores,
       capacity_memory_bytes, agent_version, created_at, updated_at, domains
FROM clusters JOIN cluster_heartbeats ON cluster_id = id
WHERE is_active = true
  AND role IN ('apps', 'all')
  AND sqlc.arg(platform)::text = ANY(platforms)
  AND health_status = 'healthy'
  AND last_heartbeat >= clock_timestamp() - make_interval(secs => sqlc.arg(silent_after_seconds)::float8)
ORDER BY region ASC, is_default DESC, created_at ASC;

-- name: ListClusterRegions :many
SELECT region, health_status, available
FROM (
    SELECT DISTINCT ON (region) region, health_status,
           COALESCE(last_heartbeat >= clock_timestamp() - make_interval(secs => sqlc.arg(silent_after_seconds)::float8), false)::boolean AS available
    FROM clusters LEFT JOIN cluster_heartbeats ON cluster_id = id
    WHERE is_active = true
      AND role IN ('apps', 'all')
      AND sqlc.arg(platform)::text = ANY(platforms)
      AND (health_status IN ('healthy', 'degraded')
           OR last_heartbeat < clock_timestamp() - make_interval(secs => sqlc.arg(silent_after_seconds)::float8)
           OR last_heartbeat IS NULL)
    ORDER BY region, available DESC, (health_status = 'healthy') DESC, is_default DESC, created_at ASC
) AS region_clusters
ORDER BY available DESC, (health_status = 'healthy') DESC, region;

-- name: ListRegionStatusesForResources :many
SELECT s.*,
       environment_status(array_agg(s.status) OVER (PARTITION BY s.resource_id, s.environment_id))::text
           AS environment_status
FROM (
    SELECT i.*,
           c.cause AS status_cause,
           region_status(c.cause, i.ready_replicas, i.desired_replicas, i.placement_status)::text AS status,
           COALESCE((SELECT d.id::text FROM deployments d
            JOIN deployment_placements dp ON dp.deployment_id = d.id AND dp.region = i.region
            WHERE d.resource_id = i.resource_id
              AND d.environment_id = i.environment_id
              AND d.id <> i.deployment_id
              AND d.status IN ('running', 'superseded')
            ORDER BY d.created_at DESC, d.id DESC
            LIMIT 1), '')::text AS last_good_deployment_id
    FROM resource_region_inputs i
    CROSS JOIN LATERAL (
        SELECT region_status_cause(
            i.last_heartbeat, i.health_status, make_interval(secs => sqlc.arg('silent_after_seconds')::float8),
            i.ready_replicas, i.desired_replicas, i.placement_status, cardinality(i.unserved_domains)
        )::text AS cause
    ) c
    WHERE i.resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
) s
ORDER BY s.resource_id, s.environment_slug, s.region;

-- name: ListEnvironmentAllocation :many
-- The six largest allocations of the workspace's resources in the environment, each row carrying
-- the totals and count over every resource with an allocation. A resource's allocation sums, over
-- its regions, desired replicas times the CPU and memory of the region in its live deployment.
-- resource_ids limits the resources; NULL reads every resource of the workspace.
WITH allocated AS (
    SELECT r.id, r.name,
           COALESCE(sum(i.desired_replicas * cpu_millicores(d.spec->'regions'->i.region->>'cpu')), 0)::bigint
               AS cpu_millicores,
           COALESCE(sum(i.desired_replicas * memory_bytes(d.spec->'regions'->i.region->>'memory')), 0)::bigint
               AS memory_bytes
    FROM resource_region_inputs i
    JOIN resources r ON r.id = i.resource_id
    JOIN deployments d ON d.id = i.deployment_id
    WHERE i.environment_id = sqlc.arg(environment_id)::uuid
      AND i.resource_id = ANY(ARRAY(
          SELECT w.id FROM resources w WHERE w.workspace_id = sqlc.arg(workspace_id)::uuid
            AND (sqlc.narg(resource_ids)::uuid[] IS NULL OR w.id = ANY(sqlc.narg(resource_ids)::uuid[]))
      ))
    GROUP BY r.id, r.name
)
SELECT a.id, a.name, a.cpu_millicores, a.memory_bytes,
       (sum(a.cpu_millicores) OVER ())::bigint AS total_cpu_millicores,
       (sum(a.memory_bytes) OVER ())::bigint AS total_memory_bytes,
       (count(*) OVER ())::int AS resource_count
FROM allocated a
WHERE a.cpu_millicores > 0 OR a.memory_bytes > 0
ORDER BY a.cpu_millicores DESC, a.memory_bytes DESC, lower(a.name), a.id
LIMIT 6;

-- name: GetWorkspaceOrganizationIDByResourceID :one
SELECT r.workspace_id, w.org_id FROM resources r JOIN workspaces w ON r.workspace_id = w.id WHERE r.id = $1;

-- name: ListWorkspaceServiceResources :many
SELECT r.id, r.workspace_id, r.slug, r.name, r.type, r.description, r.spec, r.spec_version, r.revision, r.partial, r.restart_policy, r.max_restarts, r.deploy_timeout_seconds, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1 AND r.type = 'service'
ORDER BY r.slug ASC;

-- name: WorkspaceHoldsAnyResource :one
SELECT EXISTS (
  SELECT 1 FROM resources
  WHERE workspace_id = $1 AND id = ANY(sqlc.arg('resource_ids')::uuid[])
);

-- name: BumpResourceRevision :exec
UPDATE resources SET revision = revision + 1 WHERE id = $1;

-- name: ListLatestResourceDeployments :many
SELECT DISTINCT ON (environment_id) * FROM deployments
WHERE resource_id = $1
ORDER BY environment_id, created_at DESC, id DESC;

-- name: ListWorkspaceResourceSlugs :many
SELECT r.id, r.slug
FROM resources r
WHERE r.workspace_id = $1;

-- name: SetPlacementRegionStatus :exec
UPDATE placements
SET region_status = sqlc.arg(region_status), region_status_since = NOW()
WHERE resource_id = sqlc.arg(resource_id)
  AND environment_id = sqlc.arg(environment_id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND NOT desired_deleted
  AND region_status <> sqlc.arg(region_status);

-- name: ListOrgResources :many
SELECT r.id, r.name, r.slug, r.type, r.created_at, w.id AS workspace_id, w.name AS workspace_name
FROM resources r
JOIN workspaces w ON w.id = r.workspace_id
WHERE w.org_id = sqlc.arg('org_id')
  AND (sqlc.arg('all_workspaces')::bool
       OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
       OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
  AND (sqlc.arg('query')::text = '' OR r.name ILIKE sqlc.arg('query') OR r.slug ILIKE sqlc.arg('query'))
  AND (sqlc.narg('ids')::uuid[] IS NULL OR r.id = ANY(sqlc.narg('ids')::uuid[]))
  AND (sqlc.narg('page_token')::text IS NULL
       OR (r.created_at, r.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY r.created_at DESC, r.id DESC
LIMIT sqlc.arg('row_limit');

-- name: CountOrgResources :one
SELECT COUNT(*) FROM (
  SELECT 1 FROM resources r
  JOIN workspaces w ON w.id = r.workspace_id
  WHERE w.org_id = sqlc.arg('org_id')
    AND (sqlc.arg('all_workspaces')::bool
         OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
         OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
    AND (sqlc.arg('query')::text = '' OR r.name ILIKE sqlc.arg('query') OR r.slug ILIKE sqlc.arg('query'))
    AND (sqlc.narg('ids')::uuid[] IS NULL OR r.id = ANY(sqlc.narg('ids')::uuid[]))
  LIMIT sqlc.arg('row_limit')
) capped;
