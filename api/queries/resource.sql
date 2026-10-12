-- Resource queries

-- name: CreateResource :one
INSERT INTO resources (workspace_id, name, type, description, spec, spec_version)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id;

-- name: GetResourceByID :one
SELECT r.id, r.workspace_id, r.name, r.type, r.description, r.spec, r.spec_version, r.partial, r.created_at, r.updated_at
FROM resources r
WHERE r.id = $1;

-- name: GetResourceByNameAndWorkspace :one
SELECT r.id, r.workspace_id, r.name, r.type, r.description, r.spec, r.spec_version, r.partial, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1 AND r.name = $2;

-- name: ListResourcesForWorkspace :many
SELECT r.id, r.workspace_id, r.name, r.type, r.description, r.spec, r.spec_version, r.partial, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1
   AND (sqlc.narg('page_token')::text IS NULL
        OR (r.created_at, r.id) < (
          (SELECT created_at FROM resources WHERE id = sqlc.narg('page_token')::uuid),
          sqlc.narg('page_token')::uuid
        ))
ORDER BY r.created_at DESC, r.id DESC
LIMIT $2;

-- name: UpdateResource :one
UPDATE resources
SET name = COALESCE(sqlc.narg('name'), name),
    updated_at = NOW()
WHERE id = $1
RETURNING id;

-- name: UpdateResourceSpec :exec
UPDATE resources
SET spec = $2, updated_at = NOW()
WHERE id = $1;

-- name: DeleteResource :exec
DELETE FROM resources WHERE id = $1;

-- name: SetResourcePartial :exec
UPDATE resources
SET partial = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetClusterDetails :one
SELECT id, is_active, health_status, agent_version, last_heartbeat
FROM clusters
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
       last_health_check, agent_token_hash, last_heartbeat, capacity_cpu_millicores,
       capacity_memory_bytes, agent_version, created_at, updated_at
FROM clusters
WHERE tier = sqlc.arg(tier)
  AND is_active = true
  AND health_status = 'healthy'
  AND last_heartbeat >= sqlc.arg(heard_since)::timestamptz
ORDER BY region ASC, is_default DESC, created_at ASC;

-- name: ListClustersActive :many
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status,
       last_health_check, agent_token_hash, last_heartbeat, capacity_cpu_millicores,
       capacity_memory_bytes, agent_version, created_at, updated_at
FROM clusters
WHERE is_active = true
ORDER BY region ASC;

-- name: ListRegionStatusesForResources :many
SELECT d.resource_id,
       d.environment_id,
       e.name AS environment_name,
       d.region,
       d.id AS deployment_id,
       d.status AS deployment_status,
       d.replicas AS desired_replicas,
       d.message AS deployment_message,
       (p.id IS NOT NULL)::boolean AS placed,
       COALESCE(p.ready_replicas, 0)::int AS ready_replicas,
       COALESCE(p.status_message, '')::text AS status_message,
       p.status_updated_at,
       c.last_heartbeat,
       c.health_status
FROM deployments d
JOIN environments e ON e.id = d.environment_id
JOIN clusters c ON c.id = d.cluster_id
LEFT JOIN placements p ON p.deployment_id = d.id AND NOT p.desired_deleted
WHERE d.resource_id = ANY(sqlc.arg(resource_ids)::uuid[]) AND d.is_active = true
ORDER BY d.resource_id, e.name, d.region;

-- name: GetWorkspaceOrganizationIDByResourceID :one
SELECT r.workspace_id, w.org_id FROM resources r JOIN workspaces w ON r.workspace_id = w.id WHERE r.id = $1;

-- name: ListWorkspaceServiceResources :many
SELECT r.id, r.workspace_id, r.name, r.type, r.description, r.spec, r.spec_version, r.partial, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1 AND r.type = 'service'
ORDER BY r.name ASC;
