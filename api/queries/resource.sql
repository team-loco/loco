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

-- name: ListResourcesForWorkspace :many
SELECT r.id, r.workspace_id, r.slug, r.name, r.type, r.description, r.spec, r.spec_version, r.revision, r.partial, r.restart_policy, r.max_restarts, r.deploy_timeout_seconds, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1
   AND (sqlc.narg('page_token')::text IS NULL
        OR (r.created_at, r.id) < (
          sqlc.narg('page_created_at')::timestamptz,
          sqlc.narg('page_token')::uuid
        ))
ORDER BY r.created_at DESC, r.id DESC
LIMIT $2;

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
FROM clusters
WHERE is_active = true
  AND role IN ('apps', 'all')
  AND sqlc.arg(platform)::text = ANY(platforms)
  AND health_status = 'healthy'
  AND last_heartbeat >= sqlc.arg(heard_since)::timestamptz
ORDER BY region ASC, is_default DESC, created_at ASC;

-- name: ListClusterRegions :many
SELECT region, (available AND is_default AND health_status = 'healthy')::boolean AS is_default,
       health_status, available
FROM (
    SELECT DISTINCT ON (region) region, is_default, health_status,
           COALESCE(last_heartbeat >= sqlc.arg(heard_since)::timestamptz, false)::boolean AS available
    FROM clusters
    WHERE is_active = true
      AND role IN ('apps', 'all')
      AND sqlc.arg(platform)::text = ANY(platforms)
      AND (health_status IN ('healthy', 'degraded')
           OR last_heartbeat < sqlc.arg(heard_since)::timestamptz
           OR last_heartbeat IS NULL)
    ORDER BY region, available DESC, (health_status = 'healthy') DESC, is_default DESC, created_at ASC
) AS region_clusters
ORDER BY available DESC, (health_status = 'healthy') DESC, region;

-- name: ListRegionStatusesForResources :many
SELECT l.resource_id,
       l.environment_id,
       e.slug AS environment_slug,
       l.region,
       l.cluster_id,
       l.deployment_id,
       l.status AS placement_status,
       l.replicas AS desired_replicas,
       COALESCE((SELECT ro.snapshot->'problem' FROM deployment_rollouts ro
                 WHERE ro.deployment_id = l.deployment_id AND ro.cluster_id = l.cluster_id
                 ORDER BY revision DESC LIMIT 1), 'null'::jsonb)::jsonb AS problem,
       (p.id IS NOT NULL)::boolean AS placed,
       COALESCE(p.ready_replicas, 0)::int AS ready_replicas,
       p.status_updated_at,
       COALESCE(p.region_status, '')::text AS stored_status,
       p.region_status_since,
       COALESCE((SELECT ro.replicas FROM observed_placement_rollouts ro WHERE ro.placement_id = p.id),
                '[]'::jsonb)::jsonb AS replicas,
       c.last_heartbeat,
       c.health_status,
       ARRAY(
           SELECT rd.domain FROM resource_domains rd
           WHERE rd.resource_id = l.resource_id
             AND rd.environment_id = l.environment_id
             AND rd.domain_source = 'platform_provided'
             AND NOT (rd.platform_domain = ANY(c.domains))
           ORDER BY rd.domain
       )::text[] AS unserved_domains
FROM live_deployment_placements l
JOIN environments e ON e.id = l.environment_id
JOIN clusters c ON c.id = l.cluster_id
LEFT JOIN placements p ON p.resource_id = l.resource_id AND p.environment_id = l.environment_id
    AND p.cluster_id = l.cluster_id AND NOT p.desired_deleted
WHERE l.resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
ORDER BY l.resource_id, e.slug, l.region;

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
