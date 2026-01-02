-- Deployment queries

-- name: CreateDeployment :one
INSERT INTO deployments (resource_id, resource_region_id, cluster_id, region, replicas, status, is_active, message, created_by, spec, spec_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id;

-- name: GetDeploymentByID :one
SELECT * FROM deployments WHERE id = $1;

-- name: ListDeploymentsForResource :many
SELECT * FROM deployments
WHERE resource_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountDeploymentsForResource :one
SELECT COUNT(*) FROM deployments WHERE resource_id = $1;

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
