-- App queries

-- name: CreateApp :one
INSERT INTO apps (workspace_id, cluster_id, name, namespace, type, status, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, workspace_id, cluster_id, name, namespace, type, status, created_by, created_at, updated_at;

-- name: GetAppByID :one
SELECT a.id, a.workspace_id, a.cluster_id, a.name, a.namespace, a.type, a.status, a.created_by, a.created_at, a.updated_at
FROM apps a
WHERE a.id = $1;

-- name: GetAppByNameAndWorkspace :one
SELECT a.id, a.workspace_id, a.cluster_id, a.name, a.namespace, a.type, a.status, a.created_by, a.created_at, a.updated_at
FROM apps a
WHERE a.workspace_id = $1 AND a.name = $2;

-- name: ListAppsForWorkspace :many
SELECT a.id, a.workspace_id, a.cluster_id, a.name, a.namespace, a.type, a.status, a.created_by, a.created_at, a.updated_at
FROM apps a
WHERE a.workspace_id = $1
ORDER BY a.created_at DESC;

-- name: UpdateApp :one
UPDATE apps
SET name = COALESCE(sqlc.narg('name'), name),
    updated_at = NOW()
WHERE id = $1
RETURNING id, workspace_id, cluster_id, name, namespace, type, status, created_by, created_at, updated_at;

-- name: DeleteApp :exec
DELETE FROM apps WHERE id = $1;

-- name: GetClusterDetails :one
SELECT id, is_active, health_status
FROM clusters
WHERE id = $1;

-- name: GetAppWorkspaceID :one
SELECT workspace_id FROM apps WHERE id = $1;

-- todo: eventually remove
-- name: GetFirstActiveCluster :one
SELECT id, name, region, provider, is_active, endpoint, health_status, last_health_check, created_at, updated_at, created_by
FROM clusters
WHERE is_active = true
ORDER BY created_at ASC
LIMIT 1;


