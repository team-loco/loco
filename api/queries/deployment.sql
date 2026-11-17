-- Deployment queries

-- name: CreateDeployment :one
INSERT INTO deployments (resource_id, cluster_id, region, replicas, status, is_active, spec, spec_version, environment_id, secret_names, actor_type, actor_id, method, commit_sha, commit_dirty)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING id;

-- name: GetDeploymentByID :one
SELECT * FROM deployments WHERE id = $1;

-- name: ListResourceDeployments :many
SELECT sqlc.embed(d), r.type AS resource_type,
 COALESCE((SELECT snapshot FROM deployment_rollouts ro WHERE ro.deployment_id = d.id ORDER BY revision DESC LIMIT 1), '{}'::jsonb)::jsonb AS rollout_snapshot,
 COALESCE((SELECT snapshot FROM observed_placement_rollouts ro WHERE ro.desired_deployment_id = d.id), '{}'::jsonb)::jsonb AS observed_snapshot,
 COALESCE((SELECT p.desired_revision FROM placements p WHERE p.deployment_id = d.id AND NOT p.desired_deleted LIMIT 1),
 (SELECT max(revision) FROM deployment_rollouts ro WHERE ro.deployment_id = d.id),0)::bigint AS current_revision,
       COALESCE(u.name, t.name, '')::text AS actor_name,
       COALESCE(u.avatar_url, '')::text AS actor_avatar_url
FROM deployments d
JOIN resources r ON r.id = d.resource_id
JOIN workspaces w ON w.id = r.workspace_id
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
WHERE d.resource_id = sqlc.arg('resource_id')
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

-- name: ListEnvironmentDeployments :many
SELECT sqlc.embed(d), r.type AS resource_type,
 COALESCE((SELECT snapshot FROM deployment_rollouts ro WHERE ro.deployment_id = d.id ORDER BY revision DESC LIMIT 1), '{}'::jsonb)::jsonb AS rollout_snapshot,
 COALESCE((SELECT snapshot FROM observed_placement_rollouts ro WHERE ro.desired_deployment_id = d.id), '{}'::jsonb)::jsonb AS observed_snapshot,
 COALESCE((SELECT p.desired_revision FROM placements p WHERE p.deployment_id = d.id AND NOT p.desired_deleted LIMIT 1),
 (SELECT max(revision) FROM deployment_rollouts ro WHERE ro.deployment_id = d.id),0)::bigint AS current_revision,
       COALESCE(u.name, t.name, '')::text AS actor_name,
       COALESCE(u.avatar_url, '')::text AS actor_avatar_url
FROM deployments d
JOIN resources r ON r.id = d.resource_id
JOIN workspaces w ON w.id = r.workspace_id
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
WHERE r.workspace_id = sqlc.arg('workspace_id')
  AND d.environment_id = sqlc.arg('environment_id')
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
SET status = $2, is_active = $3,
 completed_at = CASE WHEN $2::deployment_status IN ('failed','canceled','superseded') THEN COALESCE(completed_at,NOW()) ELSE NULL END,
 updated_at = NOW()
WHERE id = $1;

-- name: GetDeploymentResourceID :one
SELECT resource_id FROM deployments WHERE id = $1;

-- name: UpdateDeploymentStatus :exec
UPDATE deployments
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: ListActiveDeployments :many
SELECT resource_id FROM deployments WHERE is_active = true;

-- name: ListActiveDeploymentsForResource :many
SELECT * FROM deployments
WHERE resource_id = $1 AND is_active = true
ORDER BY created_at DESC;

-- name: AdvanceDeploymentStatus :execrows
UPDATE deployments
SET status = sqlc.arg(status),
    started_at = CASE
        WHEN sqlc.arg(status)::deployment_status = 'running' THEN COALESCE(started_at, NOW())
        ELSE started_at
    END,
    completed_at = CASE
        WHEN sqlc.arg(status)::deployment_status IN ('superseded', 'failed', 'canceled') THEN NOW()
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

-- name: GetDeploymentEvent :one
-- creator_* is NULL when the creating user, or the API token that names it, no longer exists.
SELECT d.resource_id, d.environment_id, d.region, d.spec, d.actor_type, d.actor_id,
       r.name AS resource_name, r.type AS resource_type,
       u.id AS creator_id, u.email AS creator_email, u.name AS creator_name
FROM deployments d
JOIN resources r ON r.id = d.resource_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
LEFT JOIN users u ON u.id = CASE WHEN d.actor_type = 'user' THEN d.actor_id ELSE t.created_by END
WHERE d.id = $1;

-- name: GetDeploymentView :one
SELECT sqlc.embed(d), r.type AS resource_type,
 COALESCE((SELECT snapshot FROM deployment_rollouts ro WHERE ro.deployment_id = d.id ORDER BY revision DESC LIMIT 1), '{}'::jsonb)::jsonb AS rollout_snapshot,
 COALESCE((SELECT snapshot FROM observed_placement_rollouts ro WHERE ro.desired_deployment_id = d.id), '{}'::jsonb)::jsonb AS observed_snapshot,
 COALESCE((SELECT p.desired_revision FROM placements p WHERE p.deployment_id = d.id AND NOT p.desired_deleted LIMIT 1),
 (SELECT max(revision) FROM deployment_rollouts ro WHERE ro.deployment_id = d.id),0)::bigint AS current_revision,
       COALESCE(u.name, t.name, '')::text AS actor_name,
       COALESCE(u.avatar_url, '')::text AS actor_avatar_url
FROM deployments d
JOIN resources r ON r.id = d.resource_id
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
WHERE d.id = $1;

-- name: PruneDeploymentHistory :execrows
-- Deletes the resource's deployments in the environment past the newest keep, except active
-- and unfinished ones and those a placement still runs.
DELETE FROM deployments d
WHERE d.id IN (
    SELECT old.id FROM deployments old
    WHERE old.resource_id = sqlc.arg(resource_id) AND old.environment_id = sqlc.arg(environment_id)
    ORDER BY old.created_at DESC, old.id DESC
    OFFSET sqlc.arg(keep)::int
)
  AND NOT d.is_active
  AND d.status IN ('superseded', 'failed', 'canceled')
  AND NOT EXISTS (SELECT 1 FROM placements p WHERE p.deployment_id = d.id)
  AND NOT EXISTS (
      SELECT 1 FROM observed_placement_rollouts ro
      WHERE ro.resource_id = d.resource_id AND ro.environment_id = d.environment_id
        AND ro.cluster_id = d.cluster_id
        AND (ro.reported_deployment_id = d.id OR EXISTS (
            SELECT 1 FROM jsonb_array_elements(COALESCE(ro.snapshot->'replicas', '[]'::jsonb)) replica
            WHERE replica->>'deployment_id' = d.id::text AND replica->>'ready' = 'true'
        ))
  );

-- name: SupersedePreviousDeployments :exec
UPDATE deployments old SET status = 'superseded', completed_at = NOW(), updated_at = NOW()
FROM deployments current
WHERE current.id = $1 AND old.resource_id = current.resource_id
 AND old.environment_id = current.environment_id AND old.region = current.region
 AND NOT old.is_active AND old.started_at IS NOT NULL
 AND old.status IN ('running','deploying','failed')
 AND (old.created_at, old.id) < (current.created_at, current.id);

-- name: LockPlacementResource :one
SELECT r.id FROM resources r JOIN placements p ON p.resource_id = r.id
WHERE p.id = $1 AND p.cluster_id = $2 FOR UPDATE OF r;

-- name: ListBuildingDeployments :many
SELECT * FROM deployments
WHERE spec->'build'->>'buildId' = sqlc.arg(build_id)::text
 AND status = 'building' AND is_active
ORDER BY id FOR UPDATE;

-- name: SetDeploymentBuildImage :exec
UPDATE deployments SET spec = jsonb_set(spec - 'env', '{build,image}', to_jsonb(sqlc.arg(image)::text))
WHERE id = sqlc.arg(id);

-- name: SetBuildingDeploymentEnv :exec
UPDATE deployments
SET spec = jsonb_set(spec, '{env}', COALESCE(sqlc.arg(desired_spec)::jsonb #> '{app_spec,serviceSpec,deployment,env}', '{}'::jsonb))
WHERE id = sqlc.arg(id);

-- name: PreviousDeploymentForRollback :one
SELECT previous.* FROM deployments previous JOIN deployments failed ON failed.id = $1
WHERE previous.resource_id = failed.resource_id
 AND previous.environment_id = failed.environment_id AND previous.region = failed.region
 AND (previous.created_at, previous.id) < (failed.created_at, failed.id)
 AND previous.started_at IS NOT NULL
 AND NOT previous.is_active
 AND NOT EXISTS (
 SELECT 1 FROM deployments newer
 WHERE newer.resource_id = failed.resource_id
 AND newer.environment_id = failed.environment_id AND newer.region = failed.region
 AND (newer.created_at, newer.id) > (failed.created_at, failed.id)
 )
ORDER BY previous.created_at DESC, previous.id DESC LIMIT 1 FOR UPDATE OF previous;

-- name: PlacementForDeployment :one
SELECT * FROM placements WHERE deployment_id = $1 AND NOT desired_deleted;
