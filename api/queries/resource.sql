-- Resource queries

-- name: CreateResource :one
INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: GetResourceByID :one
SELECT r.id, r.workspace_id, r.name, r.type, r.description, r.status, r.spec, r.spec_version, r.created_at, r.updated_at
FROM resources r
WHERE r.id = $1;

-- name: GetResourceByNameAndWorkspace :one
SELECT r.id, r.workspace_id, r.name, r.type, r.description, r.status, r.spec, r.spec_version, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1 AND r.name = $2;

-- name: ListResourcesForWorkspace :many
SELECT r.id, r.workspace_id, r.name, r.type, r.description, r.status, r.spec, r.spec_version, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1
   AND (sqlc.narg('page_token')::text IS NULL
        OR (r.created_at, r.id) < (
          (SELECT created_at FROM resources WHERE id = sqlc.narg('page_token')::bigint),
          sqlc.narg('page_token')::bigint
        ))
ORDER BY r.created_at DESC, r.id DESC
LIMIT $2;

-- name: UpdateResource :one
UPDATE resources
SET name = COALESCE(sqlc.narg('name'), name),
    updated_at = NOW()
WHERE id = $1
RETURNING id;

-- name: DeleteResource :exec
DELETE FROM resources WHERE id = $1;

-- name: CreateResourceRegion :one
INSERT INTO resource_regions (resource_id, region, is_primary, status)
VALUES ($1, $2, $3, $4)
RETURNING id, resource_id, region, is_primary, status, last_error, created_at, updated_at;

-- name: ListResourceRegions :many
SELECT id, resource_id, region, is_primary, status, last_error, created_at, updated_at
FROM resource_regions
WHERE resource_id = $1
ORDER BY is_primary DESC, region ASC;

-- name: GetResourceRegionByResourceAndRegion :one
SELECT id, resource_id, region, is_primary, status, last_error, created_at, updated_at
FROM resource_regions
WHERE resource_id = $1 AND region = $2;

-- name: GetClusterDetails :one
SELECT id, is_active, health_status
FROM clusters
WHERE id = $1;

-- name: GetResourceWorkspaceID :one
SELECT workspace_id FROM resources WHERE id = $1;

-- name: GetActiveClusterByRegion :one
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status, last_health_check, created_at, updated_at
FROM clusters
WHERE region = $1 AND is_active = true AND health_status = 'healthy'
ORDER BY is_default DESC, created_at ASC
LIMIT 1;

-- name: ListClustersActive :many
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status, last_health_check, created_at, updated_at
FROM clusters
WHERE is_active = true
ORDER BY region ASC;

-- todo: eventually remove
-- name: GetFirstActiveCluster :one
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status, last_health_check, created_at, updated_at
FROM clusters
WHERE is_active = true
ORDER BY created_at ASC
LIMIT 1;

-- name: ListActiveDeploymentsByResourceID :many
SELECT status FROM deployments
WHERE resource_id = $1 AND is_active = true;

-- name: UpdateResourceStatus :exec
UPDATE resources
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetWorkspaceOrganizationIDByResourceID :one
SELECT workspace_id, w.org_id FROM resources r JOIN workspaces w ON r.workspace_id = w.id WHERE r.id = $1;
