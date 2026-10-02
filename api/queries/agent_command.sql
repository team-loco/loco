-- name: SupersedeAgentCommands :execrows
UPDATE agent_commands
SET status = 'superseded', payload = NULL, updated_at = NOW()
WHERE resource_id = $1 AND cluster_id = $2 AND status IN ('pending', 'delivered');

-- name: InsertAgentCommand :one
INSERT INTO agent_commands (cluster_id, resource_id, deployment_id, type, payload, max_attempts)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id;

-- name: NotifyAgentCommands :exec
SELECT pg_notify(sqlc.arg(channel)::text, '');

-- name: ExpireAgentCommands :many
UPDATE agent_commands
SET status = 'failed',
    payload = NULL,
    last_error = 'delivery lease expired after ' || attempts || ' attempts without an ack',
    updated_at = NOW()
WHERE cluster_id = $1
  AND status = 'delivered'
  AND visible_at <= NOW()
  AND attempts >= max_attempts
RETURNING id, type, deployment_id, last_error;

-- name: ClaimAgentCommands :many
UPDATE agent_commands
SET status = 'delivered',
    attempts = attempts + 1,
    visible_at = NOW() + make_interval(secs => sqlc.arg(lease_seconds)::float8),
    updated_at = NOW()
WHERE id IN (
    SELECT c.id FROM agent_commands c
    WHERE c.cluster_id = sqlc.arg(cluster_id)
      AND c.status IN ('pending', 'delivered')
      AND c.visible_at <= NOW()
      AND c.attempts < c.max_attempts
    ORDER BY c.created_at, c.id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: GetDeliveredAgentCommandForUpdate :one
SELECT * FROM agent_commands
WHERE id = $1 AND cluster_id = $2 AND status = 'delivered'
FOR UPDATE;

-- name: CompleteAgentCommand :execrows
UPDATE agent_commands
SET status = 'succeeded', payload = NULL, last_error = NULL, acked_at = NOW(), updated_at = NOW()
WHERE id = $1 AND cluster_id = $2 AND status = 'delivered';

-- name: RetryAgentCommand :exec
UPDATE agent_commands
SET status = 'pending',
    last_error = $2,
    visible_at = NOW() + make_interval(secs => sqlc.arg(backoff_seconds)::float8),
    updated_at = NOW()
WHERE id = $1;

-- name: FailAgentCommand :exec
UPDATE agent_commands
SET status = 'failed', payload = NULL, last_error = $2, acked_at = NOW(), updated_at = NOW()
WHERE id = $1;
