-- Deployment queries

-- name: CreateDeployment :one
INSERT INTO deployments (resource_id, resource_region_id, cluster_id, region, replicas, status, is_active, message, spec, spec_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id;

-- name: GetDeploymentByID :one
SELECT * FROM deployments WHERE id = $1;

-- name: ListDeploymentsForResource :many
SELECT * FROM deployments d
WHERE d.resource_id = $1
  AND (sqlc.narg('page_token')::text IS NULL
       OR (d.created_at, d.id) < (
         (SELECT created_at FROM deployments WHERE id = sqlc.narg('page_token')::bigint),
         sqlc.narg('page_token')::bigint
       ))
ORDER BY d.created_at DESC, d.id DESC
LIMIT $2;

-- name: MarkPreviousDeploymentsNotActive :exec
UPDATE deployments
SET is_active = false, updated_at = NOW()
WHERE resource_id = $1 AND is_active = true;

-- name: GetActiveDeploymentForResourceAndRegion :one
SELECT * FROM deployments
WHERE resource_id = $1 AND region = $2 AND is_active = true
ORDER BY created_at DESC
LIMIT 1;

-- name: UpdateDeploymentStatusAndActive :exec
UPDATE deployments
SET status = $2, is_active = $3, updated_at = NOW()
WHERE id = $1;

-- name: GetDeploymentResourceID :one
SELECT resource_id FROM deployments WHERE id = $1;

-- name: UpdateDeploymentStatus :exec
UPDATE deployments
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateDeploymentStatusWithMessage :exec
UPDATE deployments
SET status = $2, message = $3, updated_at = NOW()
WHERE id = $1;

-- name: UpdateActiveDeploymentStatus :exec
UPDATE deployments
SET status = $2, message = $3, updated_at = NOW()
WHERE resource_id = $1 AND is_active = true;

-- name: ListActiveDeployments :many
SELECT resource_id FROM deployments WHERE is_active = true;

-- name: ListActiveDeploymentsForResource :many
SELECT * FROM deployments
WHERE resource_id = $1 AND is_active = true
ORDER BY created_at DESC;

-- name: MarkDeploymentNotActive :exec
UPDATE deployments
SET is_active = false, updated_at = NOW()
WHERE id = $1;
