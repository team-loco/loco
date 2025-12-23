-- Deployment queries

-- name: CreateDeployment :one
INSERT INTO deployments (resource_id, cluster_id, image, replicas, status, is_active, message, created_by, spec, spec_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

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
