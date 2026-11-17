-- name: OpenRollout :exec
INSERT INTO deployment_rollouts(deployment_id, revision)
VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: LockRollout :one
SELECT snapshot FROM deployment_rollouts WHERE deployment_id = $1 AND revision = $2 FOR UPDATE;

-- name: AllocateActivitySeq :one
UPDATE deployments SET activity_seq = activity_seq + sqlc.arg(count)::bigint
WHERE id = sqlc.arg(deployment_id) RETURNING activity_seq;

-- name: InsertDeploymentActivity :exec
INSERT INTO deployment_activity(deployment_id, seq, revision, at, severity, replica_uid, replica_name, message)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: StoreRollout :exec
UPDATE deployment_rollouts SET snapshot = $3 WHERE deployment_id = $1 AND revision = $2;

-- name: NotifyDeploymentActivity :exec
SELECT pg_notify('deployment_activity', sqlc.arg(deployment_id)::text);

-- name: ListDeploymentActivity :many
SELECT * FROM deployment_activity WHERE deployment_id = $1 AND seq > sqlc.arg(after_seq)
ORDER BY seq LIMIT sqlc.arg(page_size);
