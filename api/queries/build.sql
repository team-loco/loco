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

-- name: CancelOtherActiveBuilds :exec
UPDATE builds
SET status = 'canceled',
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE resource_id = sqlc.arg(resource_id)
  AND id <> sqlc.arg(id)
  AND status IN ('awaiting_upload', 'queued', 'running');

-- name: QueueBuild :execrows
UPDATE builds
SET status = 'queued',
    cluster_id = sqlc.arg(cluster_id),
    message = sqlc.arg(message)
WHERE id = sqlc.arg(id)
  AND status = 'awaiting_upload';

-- name: CancelBuild :execrows
UPDATE builds
SET status = 'canceled',
    message = sqlc.arg(message),
    finished_at = NOW()
WHERE id = sqlc.arg(id)
  AND status IN ('awaiting_upload', 'queued', 'running');

-- name: GetBuildCluster :one
SELECT id FROM clusters
WHERE is_active = true
ORDER BY created_at, id
LIMIT 1;

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
