-- name: OpenPlacementRollout :exec
INSERT INTO deployment_rollouts(deployment_id, revision, cause)
SELECT deployment_id, desired_revision, sqlc.arg(cause) FROM placements
WHERE id = sqlc.arg(placement_id) AND deployment_id IS NOT NULL AND NOT desired_deleted
ON CONFLICT DO NOTHING;

-- name: OpenRollout :execrows
INSERT INTO deployment_rollouts(deployment_id, revision, cause)
VALUES ($1, $2, $3) ON CONFLICT DO NOTHING;

-- name: LockRollout :one
SELECT * FROM deployment_rollouts WHERE deployment_id = $1 AND revision = $2 FOR UPDATE;

-- name: AllocateActivitySeq :one
UPDATE deployments SET activity_seq = activity_seq + sqlc.arg(count)::bigint
WHERE id = sqlc.arg(deployment_id) RETURNING activity_seq;

-- name: InsertDeploymentActivity :exec
INSERT INTO deployment_activity(deployment_id, seq, revision, at, severity, replica_uid, replica_name, message)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: StoreRollout :exec
UPDATE deployment_rollouts SET replicas = sqlc.arg(snapshot), desired_replicas = sqlc.arg(desired_replicas),
activity_count = sqlc.arg(activity_count), finished_at = COALESCE(finished_at, sqlc.narg(finished_at)::timestamptz)
WHERE deployment_id = sqlc.arg(deployment_id) AND revision = sqlc.arg(revision);

-- name: NotifyDeploymentActivity :exec
SELECT pg_notify('deployment_activity', sqlc.arg(deployment_id)::text);
