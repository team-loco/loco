-- App queries

-- name: CreateApp :one
INSERT INTO apps (workspace_id, cluster_id, name, namespace, type, subdomain, domain, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetAppByID :one
SELECT * FROM apps WHERE id = $1;

-- name: GetAppByNameAndWorkspace :one
SELECT * FROM apps WHERE workspace_id = $1 AND name = $2;

-- name: ListAppsForWorkspace :many
SELECT * FROM apps
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: UpdateApp :one
UPDATE apps
SET name = COALESCE(sqlc.narg('name'), name),
    subdomain = COALESCE(sqlc.narg('subdomain'), subdomain),
    domain = COALESCE(sqlc.narg('domain'), domain),
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteApp :exec
DELETE FROM apps WHERE id = $1;

-- name: CheckSubdomainAvailability :one
SELECT COUNT(*) = 0 AS available
FROM apps
WHERE subdomain = $1 AND domain = $2;

-- name: GetClusterDetails :one
SELECT id, is_active, health_status
FROM clusters
WHERE id = $1;

-- name: GetAppWorkspaceID :one
SELECT workspace_id FROM apps WHERE id = $1;

-- name: GetWorkspaceOrganizationIDByAppID :one
SELECT workspace_id, w.org_id FROM apps a JOIN workspaces w ON a.workspace_id = w.id WHERE a.id = $1;