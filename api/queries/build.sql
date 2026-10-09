-- name: CreateBuild :one
INSERT INTO builds (id, resource_id, status, source_type, source_key, source_size, dockerfile_path, context, image_repository, created_by)
VALUES ($1, $2, 'awaiting_upload', $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetBuildByID :one
SELECT * FROM builds WHERE id = $1;

-- name: ListBuildsForResource :many
SELECT * FROM builds b
WHERE b.resource_id = $1
  AND (sqlc.narg('page_token')::text IS NULL
       OR (b.created_at, b.id) < (
         (SELECT created_at FROM builds WHERE id = sqlc.narg('page_token')::uuid),
         sqlc.narg('page_token')::uuid
       ))
ORDER BY b.created_at DESC, b.id DESC
LIMIT $2;

-- name: CancelOtherActiveBuilds :many
UPDATE builds
SET status = 'canceled',
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE resource_id = sqlc.arg(resource_id)
  AND id <> sqlc.arg(id)
  AND status IN ('awaiting_upload', 'queued', 'running')
RETURNING id, cluster_id, source_key;

-- name: QueueBuild :execrows
UPDATE builds
SET status = 'queued',
    cluster_id = sqlc.arg(cluster_id),
    message = sqlc.arg(message)
WHERE id = sqlc.arg(id)
  AND status = 'awaiting_upload';

-- name: CancelBuild :one
UPDATE builds
SET status = 'canceled',
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE id = sqlc.arg(id)
  AND status IN ('awaiting_upload', 'queued', 'running')
RETURNING id, cluster_id, source_key;

-- name: GetBuildCluster :one
SELECT id FROM clusters
WHERE is_active = true
  AND builds_enabled = true
ORDER BY created_at, id
LIMIT 1;

-- name: ListQueuedClusterBuilds :many
SELECT b.id,
       b.resource_id,
       r.workspace_id,
       b.source_key,
       b.dockerfile_path,
       b.image_repository,
       COALESCE((
         SELECT p.image_repository || '@' || p.cache_digest
         FROM builds p
         WHERE p.resource_id = b.resource_id
           AND p.status = 'succeeded'
           AND p.cache_digest IS NOT NULL
           AND p.image_deleted_at IS NULL
           AND p.image_repository = b.image_repository
         ORDER BY p.finished_at DESC, p.id DESC
         LIMIT 1
       ), '')::text AS cache_ref
FROM builds b
JOIN resources r ON r.id = b.resource_id
WHERE b.cluster_id = $1 AND b.status = 'queued'
ORDER BY b.created_at, b.id;

-- name: ListBuildStatesByIDs :many
SELECT id, cluster_id, status FROM builds
WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: MarkBuildRunning :execrows
UPDATE builds
SET status = 'running',
    started_at = COALESCE(started_at, NOW()),
    message = sqlc.arg(message)
WHERE id = sqlc.arg(id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND status IN ('queued', 'running');

-- name: UpdateQueuedBuildMessage :execrows
UPDATE builds
SET message = sqlc.arg(message)
WHERE id = sqlc.arg(id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND status = 'queued';

-- name: FinishBuild :one
UPDATE builds
SET status = sqlc.arg(status),
    image_digest = sqlc.narg(image_digest),
    cache_digest = sqlc.narg(cache_digest),
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE id = sqlc.arg(id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND status IN ('queued', 'running')
RETURNING id, source_key;

-- name: FailMissingClusterBuilds :many
UPDATE builds
SET status = 'failed',
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE cluster_id = sqlc.arg(cluster_id)
  AND status = 'running'
  AND NOT (id = ANY(sqlc.arg(held)::uuid[]))
RETURNING id, source_key;

-- name: FailQueuedClusterBuilds :many
UPDATE builds
SET status = 'failed',
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE cluster_id = sqlc.arg(cluster_id)
  AND status = 'queued'
RETURNING id, source_key;

-- name: TryAdvisoryLock :one
SELECT pg_try_advisory_lock(sqlc.arg(lock_key)::bigint);

-- name: AdvisoryUnlock :one
SELECT pg_advisory_unlock(sqlc.arg(lock_key)::bigint);

-- name: ExpireAwaitingUploadBuilds :many
UPDATE builds
SET status = 'failed',
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE id IN (
  SELECT e.id FROM builds e
  WHERE e.status = 'awaiting_upload'
    AND e.created_at < sqlc.arg(created_before)
  ORDER BY e.created_at, e.id
  LIMIT sqlc.arg(max_builds)
)
  AND status = 'awaiting_upload'
RETURNING id;

-- name: ListUndeletedBuildSources :many
SELECT id, source_key FROM builds
WHERE source_deleted_at IS NULL
  AND status IN ('succeeded', 'failed', 'canceled')
ORDER BY finished_at, id
LIMIT sqlc.arg(max_builds);

-- name: MarkBuildSourceDeleted :exec
UPDATE builds
SET source_deleted_at = NOW()
WHERE id = $1
  AND source_deleted_at IS NULL;

-- name: ListActiveBuildSourceKeys :many
SELECT source_key FROM builds
WHERE source_key = ANY(sqlc.arg(keys)::text[])
  AND status IN ('awaiting_upload', 'queued', 'running');

-- name: ListDeletableBuildImages :many
WITH ranked AS (
  SELECT b.id,
         b.resource_id,
         b.image_repository,
         b.image_digest,
         b.cache_digest,
         b.finished_at,
         b.image_deleted_at,
         row_number() OVER (PARTITION BY b.resource_id ORDER BY b.finished_at DESC, b.id DESC) AS position
  FROM builds b
  WHERE b.status = 'succeeded'
    AND (sqlc.narg(build_id)::uuid IS NULL
         OR b.resource_id = (SELECT o.resource_id FROM builds o WHERE o.id = sqlc.narg(build_id)::uuid))
)
SELECT c.id,
       c.resource_id,
       c.image_repository,
       c.image_digest::text AS image_digest,
       c.cache_digest
FROM ranked c
WHERE c.position > sqlc.arg(keep)::int
  AND c.image_deleted_at IS NULL
  AND (sqlc.narg(build_id)::uuid IS NULL OR c.id = sqlc.narg(build_id)::uuid)
  AND NOT EXISTS (
    SELECT 1 FROM ranked k
    WHERE k.resource_id = c.resource_id
      AND k.position <= sqlc.arg(keep)::int
      AND (k.image_digest IN (c.image_digest, c.cache_digest)
           OR k.cache_digest IN (c.image_digest, c.cache_digest))
  )
  AND NOT EXISTS (
    SELECT 1 FROM deployments d
    WHERE d.resource_id = c.resource_id
      AND d.is_active
      AND strpos(d.spec::text, c.image_digest) > 0
  )
  AND NOT EXISTS (
    SELECT 1 FROM placements p
    WHERE p.resource_id = c.resource_id
      AND NOT p.desired_deleted
      AND strpos(p.desired_spec::text, c.image_digest) > 0
  )
ORDER BY c.finished_at, c.id
LIMIT sqlc.arg(max_builds);

-- name: LockBuildImageForDelete :one
SELECT image_deleted_at FROM builds WHERE id = $1 FOR UPDATE;

-- name: LockBuildImageForDeploy :one
SELECT image_deleted_at FROM builds WHERE id = $1 FOR SHARE;

-- name: MarkBuildImageDeleted :execrows
UPDATE builds
SET image_deleted_at = NOW()
WHERE id = $1
  AND image_deleted_at IS NULL;

-- name: ListExistingResourceIDs :many
SELECT id FROM resources WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListBuildTagStates :many
SELECT id, resource_id, status, finished_at FROM builds
WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListLiveBuildDigests :many
SELECT resource_id, image_digest::text AS image_digest, cache_digest FROM builds
WHERE resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
  AND status = 'succeeded'
  AND image_deleted_at IS NULL;
