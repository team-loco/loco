-- Deployment queries

-- name: CreateDeployment :one
INSERT INTO deployments (resource_id, status, spec, spec_version, environment_id, plain_env, secret_env, actor_type, actor_id, method, commit_sha, commit_dirty, trigger, source_deployment_id, traceparent)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, sqlc.narg(traceparent))
RETURNING id;

-- name: CreateDeploymentPlacement :exec
INSERT INTO deployment_placements (deployment_id, cluster_id, region, replicas, status, image)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetDeploymentByID :one
SELECT * FROM deployments WHERE id = $1;

-- name: ListDeploymentPlacements :many
SELECT * FROM deployment_placements WHERE deployment_id = $1 ORDER BY region, cluster_id;

-- name: ListResourceDeployments :many
SELECT sqlc.embed(d), r.type AS resource_type, r.name AS resource_name,
       deployment_placements_json(d.id)::jsonb AS placements,
       COALESCE(u.name, t.name, '')::text AS actor_name,
       COALESCE(u.avatar_url, '')::text AS actor_avatar_url,
       COALESCE(cu.name, ct.name, '')::text AS canceled_by_name,
       COALESCE(b.git_commit_message, '')::text AS commit_message
FROM deployments d
JOIN resources r ON r.id = d.resource_id
JOIN workspaces w ON w.id = r.workspace_id
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
LEFT JOIN users cu ON d.canceled_by_type = 'user' AND cu.id = d.canceled_by_id
LEFT JOIN api_tokens ct ON d.canceled_by_type = 'api_token' AND ct.id = d.canceled_by_id
LEFT JOIN builds b ON b.id = (d.spec->'build'->>'buildId')::uuid
WHERE d.resource_id = sqlc.arg('resource_id')
  AND (sqlc.narg('environment_id')::uuid IS NULL OR d.environment_id = sqlc.narg('environment_id'))
  AND (sqlc.arg('system_read')::bool
       OR w.org_id = ANY(sqlc.arg('organization_ids')::uuid[])
       OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
       OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
  AND (sqlc.narg('since')::timestamptz IS NULL OR d.created_at >= sqlc.narg('since'))
  AND (sqlc.narg('until')::timestamptz IS NULL OR d.created_at < sqlc.narg('until'))
  AND (COALESCE(cardinality(sqlc.arg('statuses')::text[]), 0) = 0 OR d.status::text = ANY(sqlc.arg('statuses')::text[]))
  AND (sqlc.narg('build_id')::text IS NULL OR d.spec->'build'->>'buildId' = sqlc.narg('build_id'))
  AND (sqlc.narg('page_token')::text IS NULL
       OR (d.created_at, d.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY d.created_at DESC, d.id DESC
LIMIT sqlc.arg('page_size');

-- name: ListEnvironmentDeployments :many
SELECT sqlc.embed(d), r.type AS resource_type, r.name AS resource_name,
       deployment_placements_json(d.id)::jsonb AS placements,
       COALESCE(u.name, t.name, '')::text AS actor_name,
       COALESCE(u.avatar_url, '')::text AS actor_avatar_url,
       COALESCE(cu.name, ct.name, '')::text AS canceled_by_name,
       COALESCE(b.git_commit_message, '')::text AS commit_message
FROM deployments d
JOIN resources r ON r.id = d.resource_id
JOIN workspaces w ON w.id = r.workspace_id
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
LEFT JOIN users cu ON d.canceled_by_type = 'user' AND cu.id = d.canceled_by_id
LEFT JOIN api_tokens ct ON d.canceled_by_type = 'api_token' AND ct.id = d.canceled_by_id
LEFT JOIN builds b ON b.id = (d.spec->'build'->>'buildId')::uuid
WHERE r.workspace_id = sqlc.arg('workspace_id')
  AND d.environment_id = sqlc.arg('environment_id')
  AND (sqlc.arg('system_read')::bool
       OR w.org_id = ANY(sqlc.arg('organization_ids')::uuid[])
       OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
       OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
  AND (sqlc.narg('since')::timestamptz IS NULL OR d.created_at >= sqlc.narg('since'))
  AND (sqlc.narg('until')::timestamptz IS NULL OR d.created_at < sqlc.narg('until'))
  AND (COALESCE(cardinality(sqlc.arg('statuses')::text[]), 0) = 0 OR d.status::text = ANY(sqlc.arg('statuses')::text[]))
  AND (sqlc.narg('build_id')::text IS NULL OR d.spec->'build'->>'buildId' = sqlc.narg('build_id'))
  AND (sqlc.narg('page_token')::text IS NULL
       OR (d.created_at, d.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY d.created_at DESC, d.id DESC
LIMIT sqlc.arg('page_size');

-- name: CountDeployments :one
SELECT COUNT(*) FROM (
  SELECT 1 FROM deployments d
  JOIN resources r ON r.id = d.resource_id
  JOIN workspaces w ON w.id = r.workspace_id
  WHERE (sqlc.narg('resource_id')::uuid IS NULL OR d.resource_id = sqlc.narg('resource_id'))
    AND (sqlc.narg('workspace_id')::uuid IS NULL OR r.workspace_id = sqlc.narg('workspace_id'))
    AND (sqlc.narg('environment_id')::uuid IS NULL OR d.environment_id = sqlc.narg('environment_id'))
    AND (sqlc.arg('system_read')::bool
         OR w.org_id = ANY(sqlc.arg('organization_ids')::uuid[])
         OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
         OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
    AND (sqlc.narg('since')::timestamptz IS NULL OR d.created_at >= sqlc.narg('since'))
    AND (sqlc.narg('until')::timestamptz IS NULL OR d.created_at < sqlc.narg('until'))
    AND (COALESCE(cardinality(sqlc.arg('statuses')::text[]), 0) = 0 OR d.status::text = ANY(sqlc.arg('statuses')::text[]))
    AND (sqlc.narg('build_id')::text IS NULL OR d.spec->'build'->>'buildId' = sqlc.narg('build_id'))
  LIMIT sqlc.arg('row_limit')
) capped;

-- name: QueryDeploymentBuckets :many
-- Splits [since, until) into bucket_count equal buckets by creation time, in exact numeric arithmetic.
WITH matched AS (
  SELECT d.id, d.status, d.created_at, d.resource_id, r.name AS resource_name,
         COALESCE(d.spec->'build'->>'image', '') AS image,
         CASE WHEN sqlc.arg('group_by_resource')::bool THEN d.resource_id ELSE '00000000-0000-0000-0000-000000000000'::uuid END AS group_id,
         div(EXTRACT(EPOCH FROM d.created_at - sqlc.arg('since')::timestamptz) * sqlc.arg('bucket_count')::int,
             EXTRACT(EPOCH FROM sqlc.arg('until')::timestamptz - sqlc.arg('since')::timestamptz)) AS bucket
  FROM deployments d
  JOIN resources r ON r.id = d.resource_id
  JOIN workspaces w ON w.id = r.workspace_id
  WHERE r.workspace_id = sqlc.arg('workspace_id')
    AND d.environment_id = sqlc.arg('environment_id')
    AND (COALESCE(cardinality(sqlc.arg('filter_resource_ids')::uuid[]), 0) = 0 OR d.resource_id = ANY(sqlc.arg('filter_resource_ids')::uuid[]))
    AND (sqlc.arg('system_read')::bool
         OR w.org_id = ANY(sqlc.arg('organization_ids')::uuid[])
         OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
         OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
    AND d.created_at >= sqlc.arg('since')
    AND d.created_at < sqlc.arg('until')
),
statuses AS (
  SELECT group_id, bucket, jsonb_object_agg(status, n) AS counts
  FROM (SELECT group_id, bucket, status, COUNT(*) AS n FROM matched GROUP BY group_id, bucket, status) s
  GROUP BY group_id, bucket
),
names AS (
  SELECT group_id, bucket, COUNT(*) AS resource_count,
         array_agg(resource_name ORDER BY latest DESC, resource_name) AS resource_names
  FROM (SELECT group_id, bucket, resource_name, MAX(created_at) AS latest
        FROM matched GROUP BY group_id, bucket, resource_name) n
  GROUP BY group_id, bucket
),
totals AS (
  SELECT group_id, bucket, COUNT(*) AS deployment_count,
         to_timestamp(AVG(EXTRACT(EPOCH FROM created_at))::float8) AS mean_created_at,
         (array_agg(id ORDER BY created_at DESC, id DESC))[1] AS latest_id
  FROM matched
  GROUP BY group_id, bucket
)
SELECT t.group_id, t.bucket::int AS bucket, t.deployment_count::int AS deployment_count,
       t.mean_created_at::timestamptz AS mean_created_at, s.counts::jsonb AS status_counts,
       n.resource_count::int AS resource_count,
       n.resource_names[1:sqlc.arg('max_resource_names')::int]::text[] AS resource_names,
       m.id AS latest_id, m.resource_id AS latest_resource_id, m.resource_name AS latest_resource_name,
       m.status AS latest_status, m.created_at AS latest_created_at, m.image::text AS latest_image
FROM totals t
JOIN statuses s ON s.group_id = t.group_id AND s.bucket = t.bucket
JOIN names n ON n.group_id = t.group_id AND n.bucket = t.bucket
JOIN matched m ON m.id = t.latest_id
ORDER BY t.group_id, t.bucket;

-- name: GetDeploymentView :one
SELECT sqlc.embed(d), r.type AS resource_type, r.name AS resource_name,
       deployment_placements_json(d.id)::jsonb AS placements,
       COALESCE(u.name, t.name, '')::text AS actor_name,
       COALESCE(u.avatar_url, '')::text AS actor_avatar_url,
       COALESCE(cu.name, ct.name, '')::text AS canceled_by_name,
       COALESCE(b.git_commit_message, '')::text AS commit_message
FROM deployments d
JOIN resources r ON r.id = d.resource_id
LEFT JOIN users u ON d.actor_type = 'user' AND u.id = d.actor_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
LEFT JOIN users cu ON d.canceled_by_type = 'user' AND cu.id = d.canceled_by_id
LEFT JOIN api_tokens ct ON d.canceled_by_type = 'api_token' AND ct.id = d.canceled_by_id
LEFT JOIN builds b ON b.id = (d.spec->'build'->>'buildId')::uuid
WHERE d.id = $1;

-- name: GetDeploymentResourceID :one
SELECT resource_id FROM deployments WHERE id = $1;

-- name: ListLiveDeploymentsForResource :many
SELECT sqlc.embed(d), l.cluster_id, l.region, l.replicas, l.status AS placement_status
FROM live_deployment_placements l
JOIN deployments d ON d.id = l.deployment_id
WHERE l.resource_id = $1
ORDER BY l.environment_id, d.created_at DESC, d.id DESC, l.region, l.cluster_id;

-- name: ListDesiredDeploymentsForEnvironment :many
SELECT sqlc.embed(d), l.cluster_id, l.region, l.replicas, l.status AS placement_status
FROM desired_deployment_placements l
JOIN deployments d ON d.id = l.deployment_id
WHERE l.environment_id = $1
ORDER BY l.resource_id, d.created_at DESC, d.id DESC, l.region, l.cluster_id;

-- name: GetLiveDeploymentForRegion :one
SELECT sqlc.embed(d), l.cluster_id, l.region, l.replicas, l.status AS placement_status
FROM live_deployment_placements l
JOIN deployments d ON d.id = l.deployment_id
WHERE l.resource_id = $1 AND l.environment_id = $2 AND l.region = $3
ORDER BY d.created_at DESC, d.id DESC
LIMIT 1;

-- name: ListResourcesRunningOutsideEnvironment :many
SELECT DISTINCT resource_id FROM live_deployment_placements
WHERE resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
  AND environment_id <> sqlc.arg(environment_id);

-- name: SetDeploymentPlacementStatus :execrows
UPDATE deployment_placements
SET status = sqlc.arg(status),
    completed_at = CASE
        WHEN sqlc.arg(status)::placement_status IN ('failed', 'canceled', 'superseded') THEN COALESCE(completed_at, NOW())
        ELSE NULL
    END,
    updated_at = NOW()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND status::text = ANY(sqlc.arg(from_statuses)::text[]);

-- name: DeployDeploymentPlacement :execrows
-- Records that a running placement's current revision became healthy with every replica available.
UPDATE deployment_placements
SET deployed = true, started_at = COALESCE(started_at, NOW()), updated_at = NOW()
WHERE deployment_id = sqlc.arg(deployment_id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND status = 'running'
  AND NOT deployed;

-- name: SupersedePlacements :many
-- Retires the older deployments' placements in the cluster's region: one that never ran is canceled,
-- one that ran is superseded. Without include_started only the never-started ones change. The
-- placement the cluster still runs stays until the deployment is placed there.
UPDATE deployment_placements old
SET status = CASE WHEN old.started_at IS NULL THEN 'canceled' ELSE 'superseded' END::placement_status,
    superseded_by = current.id,
    completed_at = NOW(),
    updated_at = NOW()
FROM deployments current, deployments previous
WHERE current.id = sqlc.arg(deployment_id)
  AND previous.id = old.deployment_id
  AND old.region = (
      SELECT target.region FROM deployment_placements target
      WHERE target.deployment_id = current.id AND target.cluster_id = sqlc.arg(cluster_id)
  )
  AND previous.resource_id = current.resource_id
  AND previous.environment_id = current.environment_id
  AND (previous.created_at, previous.id) < (current.created_at, current.id)
  AND old.status IN ('pending', 'deploying', 'running', 'degraded', 'crashed')
  AND (sqlc.arg(include_started)::bool OR old.started_at IS NULL)
  AND NOT EXISTS (
      SELECT 1 FROM placements p
      WHERE p.resource_id = current.resource_id AND p.environment_id = current.environment_id
        AND p.cluster_id = old.cluster_id AND p.deployment_id = old.deployment_id AND NOT p.desired_deleted
  )
RETURNING old.deployment_id;

-- name: RecomputeDeploymentStatus :one
WITH previous AS (
    SELECT locked.id, locked.status, locked.ready FROM deployments locked WHERE locked.id = $1 FOR UPDATE
), next AS (
    SELECT status, status IN ('running', 'degraded') AND NOT EXISTS (
        SELECT 1 FROM deployment_placements dp
        WHERE dp.deployment_id = $1 AND dp.status IN ('running', 'degraded') AND NOT dp.deployed
    ) AS ready
    FROM (SELECT deployment_status_from_placements($1) AS status) s
)
UPDATE deployments d
SET status = next.status,
    ready = next.ready,
    started_at = CASE WHEN next.ready THEN COALESCE(d.started_at, NOW()) ELSE d.started_at END,
    completed_at = CASE
        WHEN next.status IN ('failed', 'canceled', 'superseded') THEN COALESCE(d.completed_at, NOW())
        ELSE NULL
    END,
    updated_at = CASE WHEN next.status <> previous.status THEN NOW() ELSE d.updated_at END
FROM previous, next
WHERE d.id = previous.id
RETURNING previous.status AS previous_status, d.status, previous.ready AS previously_ready, d.ready;

-- name: GetDeploymentEvent :one
-- creator_* is NULL when the creating user, or the API token that names it, no longer exists.
SELECT d.resource_id, d.environment_id, d.spec, d.actor_type, d.actor_id,
       r.name AS resource_name, r.type AS resource_type,
       u.id AS creator_id, u.email AS creator_email, u.name AS creator_name
FROM deployments d
JOIN resources r ON r.id = d.resource_id
LEFT JOIN api_tokens t ON d.actor_type = 'api_token' AND t.id = d.actor_id
LEFT JOIN users u ON u.id = CASE WHEN d.actor_type = 'user' THEN d.actor_id ELSE t.created_by END
WHERE d.id = $1;

-- name: PruneDeploymentHistory :execrows
-- Deletes the resource's deployments in the environment past the newest keep, except unfinished
-- ones and those a region still runs or reports.
DELETE FROM deployments d
WHERE d.id IN (
    SELECT old.id FROM deployments old
    WHERE old.resource_id = sqlc.arg(resource_id) AND old.environment_id = sqlc.arg(environment_id)
    ORDER BY old.created_at DESC, old.id DESC
    OFFSET sqlc.arg(keep)::int
)
  AND d.status IN ('superseded', 'failed', 'canceled')
  AND NOT EXISTS (SELECT 1 FROM desired_deployment_placements target WHERE target.deployment_id = d.id)
  AND NOT EXISTS (SELECT 1 FROM placements p WHERE p.deployment_id = d.id)
  AND NOT EXISTS (
      SELECT 1 FROM observed_placement_rollouts ro
      WHERE ro.resource_id = d.resource_id AND ro.environment_id = d.environment_id
        AND (ro.reported_deployment_id = d.id OR EXISTS (
            SELECT 1 FROM jsonb_array_elements(COALESCE(ro.snapshot->'replicas', '[]'::jsonb)) replica
            WHERE replica->>'deployment_id' = d.id::text AND replica->>'state' NOT IN ('Stopped', 'Killed')
        ))
  );

-- name: LockPlacementResource :one
SELECT r.id FROM resources r JOIN placements p ON p.resource_id = r.id
WHERE p.id = $1 AND p.cluster_id = $2 FOR UPDATE OF r;

-- name: ListBuildingDeployments :many
SELECT * FROM deployments
WHERE spec->'build'->>'buildId' = sqlc.arg(build_id)::text
 AND status = 'building'
ORDER BY id FOR UPDATE;

-- name: SetDeploymentBuildImage :exec
WITH placed AS (
    UPDATE deployment_placements SET image = sqlc.arg(image)::text, updated_at = NOW()
    WHERE deployment_id = sqlc.arg(id) AND status = 'pending'
)
UPDATE deployments SET spec = jsonb_set(spec, '{build,image}', to_jsonb(sqlc.arg(image)::text))
WHERE id = sqlc.arg(id);

-- name: GetDeploymentPlacement :one
SELECT * FROM deployment_placements WHERE deployment_id = $1 AND cluster_id = $2;

-- name: LockDeployment :exec
SELECT id FROM deployments WHERE id = $1 FOR UPDATE;

-- name: PreviousRunningDeploymentForCluster :one
-- The newest deployment older than the given one whose placement on the cluster ran and still
-- serves, including one a restart rolls again.
SELECT previous.* FROM deployment_placements dp
JOIN deployments previous ON previous.id = dp.deployment_id
JOIN deployments current ON current.id = sqlc.arg(deployment_id)
WHERE dp.cluster_id = sqlc.arg(cluster_id)
  AND previous.resource_id = current.resource_id
  AND previous.environment_id = current.environment_id
  AND (previous.created_at, previous.id) < (current.created_at, current.id)
  AND dp.started_at IS NOT NULL
  AND dp.status IN ('deploying', 'running', 'degraded', 'crashed')
ORDER BY previous.created_at DESC, previous.id DESC
LIMIT 1;

-- name: PreviousLiveDeploymentForCluster :one
-- The newest deployment older than the given one whose placement on the cluster served and did
-- not fail.
SELECT previous.* FROM deployment_placements dp
JOIN deployments previous ON previous.id = dp.deployment_id
JOIN deployments current ON current.id = sqlc.arg(deployment_id)
WHERE dp.cluster_id = sqlc.arg(cluster_id)
  AND previous.resource_id = current.resource_id
  AND previous.environment_id = current.environment_id
  AND (previous.created_at, previous.id) < (current.created_at, current.id)
  AND dp.started_at IS NOT NULL
  AND dp.status IN ('running', 'degraded', 'crashed', 'superseded')
ORDER BY previous.created_at DESC, previous.id DESC
LIMIT 1;

-- name: SetDeploymentCanceledBy :exec
UPDATE deployments SET canceled_by_type = sqlc.arg(actor_type), canceled_by_id = sqlc.arg(actor_id)
WHERE id = sqlc.arg(id);

-- name: BuildUsedByOtherDeployments :one
SELECT EXISTS (
    SELECT 1 FROM deployments
    WHERE spec->'build'->>'buildId' = sqlc.arg(build_id)::text
      AND status IN ('building', 'deploying')
      AND id <> sqlc.arg(deployment_id)
);
