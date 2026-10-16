-- name: CreateEnvironment :one
INSERT INTO environments (workspace_id, name, description, environment_type, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetEnvironmentByID :one
SELECT * FROM environments WHERE id = $1;

-- name: GetWorkspaceProductionEnvironment :one
SELECT * FROM environments WHERE workspace_id = $1 AND environment_type = 'production' ORDER BY created_at ASC LIMIT 1;

-- name: ListWorkspaceEnvironments :many
SELECT * FROM environments WHERE workspace_id = $1 ORDER BY created_at ASC;

-- name: UpdateEnvironment :one
UPDATE environments
SET name = $2, description = $3, environment_type = $4, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteEnvironment :exec
DELETE FROM environments WHERE id = $1;

-- name: CountDeploymentsByEnvironment :one
SELECT COUNT(*) FROM deployments WHERE environment_id = $1;

-- name: BumpEnvironmentRevision :exec
UPDATE environments
SET revision = revision + 1
WHERE id = $1;

-- name: BumpResourceEnvironmentRevisions :exec
UPDATE environments
SET revision = revision + 1
WHERE workspace_id = (SELECT r.workspace_id FROM resources r WHERE r.id = $1);

-- name: LockWorkspaceEnvironments :many
WITH workspace_lock AS MATERIALIZED (
    SELECT w.id FROM workspaces w WHERE w.id = $1 FOR KEY SHARE OF w
)
SELECT e.id, e.revision FROM environments e
JOIN workspace_lock w ON w.id = e.workspace_id
ORDER BY e.id
FOR UPDATE OF e;

-- name: LockResourceEnvironments :many
WITH workspace_lock AS MATERIALIZED (
    SELECT w.id FROM workspaces w
    JOIN resources r ON r.workspace_id = w.id
    WHERE r.id = $1
    FOR KEY SHARE OF w
)
SELECT e.id, e.revision FROM environments e
JOIN workspace_lock w ON w.id = e.workspace_id
ORDER BY e.id
FOR UPDATE OF e;
