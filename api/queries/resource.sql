-- Resource queries

-- name: CreateResource :one
INSERT INTO resources (workspace_id, cluster_id, name, namespace, type, status, spec, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, workspace_id, cluster_id, name, namespace, type, status, spec, created_by, created_at, updated_at;

-- name: GetResourceByID :one
SELECT r.id, r.workspace_id, r.cluster_id, r.name, r.namespace, r.type, r.status, r.spec, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.id = $1;

-- name: GetResourceByNameAndWorkspace :one
SELECT r.id, r.workspace_id, r.cluster_id, r.name, r.namespace, r.type, r.status, r.spec, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1 AND r.name = $2;

-- name: ListResourcesForWorkspace :many
SELECT r.id, r.workspace_id, r.cluster_id, r.name, r.namespace, r.type, r.status, r.spec, r.created_by, r.created_at, r.updated_at
FROM resources r
WHERE r.workspace_id = $1
ORDER BY r.created_at DESC;

-- name: UpdateResource :one
UPDATE resources
SET name = COALESCE(sqlc.narg('name'), name),
    updated_at = NOW()
WHERE id = $1
RETURNING id, workspace_id, cluster_id, name, namespace, type, status, spec, created_by, created_at, updated_at;

-- name: DeleteResource :exec
DELETE FROM resources WHERE id = $1;

-- name: GetClusterDetails :one
SELECT id, is_active, health_status
FROM clusters
WHERE id = $1;

-- name: GetResourceWorkspaceID :one
SELECT workspace_id FROM resources WHERE id = $1;

-- todo: eventually remove
-- name: GetFirstActiveCluster :one
SELECT id, name, region, provider, is_active, endpoint, health_status, last_health_check, created_at, updated_at, created_by
FROM clusters
WHERE is_active = true
ORDER BY created_at ASC
LIMIT 1;
