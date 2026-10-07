-- name: CreateBuild :one
INSERT INTO builds (id, resource_id, status, source_type, source_key, source_size, dockerfile_path, image_repository, created_by)
VALUES ($1, $2, 'awaiting_upload', $3, $4, $5, $6, $7, $8)
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

-- name: LockResourceForBuild :one
SELECT id FROM resources WHERE id = $1 FOR UPDATE;

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

-- name: TryLockSourceSweep :one
SELECT pg_try_advisory_lock(sqlc.arg(lock_key)::bigint);

-- name: UnlockSourceSweep :one
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
