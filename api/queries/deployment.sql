-- Deployment queries

-- name: CreateDeployment :one
INSERT INTO deployments (resource_id, cluster_id, region, replicas, status, is_active, message, spec, spec_version, environment_id, secret_names, actor_type, actor_id, method, commit_sha, commit_dirty, started_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, NOW())
RETURNING id;

-- name: GetDeploymentByID :one
SELECT * FROM deployments WHERE id = $1;

-- name: GetDeploymentStatus :one
SELECT status, message FROM deployments WHERE id = $1;

-- name: ListDeployments :many
SELECT sqlc.embed(d), r.type AS resource_type,
       COALESCE(u.name, t.name, '')::text AS actor_name,
       COALESCE(u.avatar_url, '')::text AS actor_avatar_url
FROM deployments d
JOIN resources r ON r.id = d.resource_id
JOIN workspaces w ON w.id = r.workspace_id
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
WHERE (sqlc.narg('resource_id')::uuid IS NULL OR d.resource_id = sqlc.narg('resource_id'))
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR r.workspace_id = sqlc.narg('workspace_id'))
  AND (sqlc.narg('environment_id')::uuid IS NULL OR d.environment_id = sqlc.narg('environment_id'))
  AND (sqlc.arg('system_read')::bool
       OR w.org_id = ANY(sqlc.arg('organization_ids')::uuid[])
       OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
       OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
  AND (sqlc.narg('since')::timestamptz IS NULL OR d.created_at >= sqlc.narg('since'))
  AND (sqlc.narg('until')::timestamptz IS NULL OR d.created_at < sqlc.narg('until'))
  AND (sqlc.narg('page_token')::text IS NULL
       OR (d.created_at, d.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY d.created_at DESC, d.id DESC
LIMIT sqlc.arg('page_size');

-- name: MarkPreviousDeploymentsNotActive :exec
UPDATE deployments
SET is_active = false, updated_at = NOW()
WHERE resource_id = $1 AND is_active = true;

-- name: GetActiveDeploymentForResourceAndRegion :one
SELECT * FROM deployments
WHERE resource_id = $1 AND environment_id = $2 AND region = $3 AND is_active = true
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

-- name: AdvanceDeploymentStatus :exec
UPDATE deployments
SET status = sqlc.arg(status),
    message = sqlc.arg(message),
    completed_at = CASE
        WHEN sqlc.arg(status)::deployment_status IN ('succeeded', 'failed', 'canceled') THEN NOW()
        ELSE NULL
    END,
    updated_at = NOW()
WHERE id = sqlc.arg(id)
  AND is_active = true
  AND status::text = ANY(sqlc.arg(from_statuses)::text[]);

-- name: ListResourcesRunningOutsideEnvironment :many
SELECT DISTINCT resource_id FROM deployments
WHERE resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
  AND environment_id <> sqlc.arg(environment_id)
  AND is_active = true;

-- name: ListActiveDeploymentsForEnvironment :many
SELECT * FROM deployments
WHERE environment_id = $1 AND is_active = true
ORDER BY resource_id, created_at DESC;

-- name: GetDeploymentActor :one
SELECT COALESCE(u.name, t.name, '')::text AS name,
       COALESCE(u.avatar_url, '')::text AS avatar_url
FROM deployments d
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
WHERE d.id = $1;
