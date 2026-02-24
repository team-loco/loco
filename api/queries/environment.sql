-- name: CreateEnvironment :one
INSERT INTO environments (org_id, name, description, is_production, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetEnvironmentByID :one
SELECT * FROM environments WHERE id = $1;

-- name: GetEnvironmentByName :one
SELECT * FROM environments WHERE org_id = $1 AND name = $2;

-- name: GetOrgProductionEnvironment :one
SELECT * FROM environments WHERE org_id = $1 AND is_production = true ORDER BY created_at ASC LIMIT 1;

-- name: ListOrgEnvironments :many
SELECT * FROM environments WHERE org_id = $1 ORDER BY created_at ASC;

-- name: UpdateEnvironment :one
UPDATE environments
SET name = $2, description = $3, is_production = $4, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteEnvironment :exec
DELETE FROM environments WHERE id = $1;

-- name: CountResourcesByEnvironment :one
SELECT COUNT(*) FROM resources WHERE environment_id = $1;
