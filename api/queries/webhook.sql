-- name: CreateWorkspaceWebhook :one
INSERT INTO webhooks (kind, workspace_id, url, secret, event_types, created_by)
VALUES ('workspace', sqlc.arg('workspace_id')::uuid, sqlc.arg('url'), sqlc.arg('secret'), sqlc.arg('event_types'), sqlc.narg('created_by'))
RETURNING *;

-- name: ListWorkspaceWebhooks :many
SELECT * FROM webhooks
WHERE kind = 'workspace' AND workspace_id = sqlc.arg('workspace_id')::uuid
ORDER BY created_at;

-- name: CountWorkspaceWebhooks :one
SELECT count(*) FROM webhooks WHERE kind = 'workspace' AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: GetWorkspaceWebhook :one
SELECT * FROM webhooks
WHERE id = sqlc.arg('id') AND kind = 'workspace' AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: DeleteWorkspaceWebhook :execrows
DELETE FROM webhooks
WHERE id = sqlc.arg('id') AND kind = 'workspace' AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: ListWebhookDeliveries :many
SELECT d.id, d.status, d.attempts, d.next_attempt_at, d.last_status_code, d.last_error, d.created_at, d.delivered_at,
       e.type AS event_type, e.id AS event_id
FROM webhook_deliveries d
JOIN events e ON e.seq = d.event_seq
WHERE d.webhook_id = $1
ORDER BY d.created_at DESC
LIMIT sqlc.arg('max_rows');

-- name: ClaimWebhookDeliveries :many
UPDATE webhook_deliveries d
SET next_attempt_at = NOW() + make_interval(secs => sqlc.arg('lease_seconds')::int),
    attempts = d.attempts + 1
WHERE d.id IN (
    SELECT q.id
    FROM webhook_deliveries q
    WHERE q.status = 'pending' AND q.next_attempt_at <= NOW()
    ORDER BY q.next_attempt_at
    LIMIT sqlc.arg('max_rows')
    FOR UPDATE SKIP LOCKED
)
RETURNING d.id, d.attempts;

-- name: GetWebhookDeliveryPayload :one
SELECT d.id, w.url, w.secret, w.kind,
       e.seq, e.id AS event_id, e.type, e.org_id, e.workspace_id, e.actor_type, e.actor_id,
       e.subject_type, e.subject_id, e.request_id, e.data, e.created_at
FROM webhook_deliveries d
JOIN webhooks w ON w.id = d.webhook_id
JOIN events e ON e.seq = d.event_seq
WHERE d.id = $1;

-- name: FinishWebhookDelivery :exec
UPDATE webhook_deliveries
SET status = sqlc.arg('status'),
    last_status_code = sqlc.narg('last_status_code'),
    last_error = sqlc.narg('last_error'),
    next_attempt_at = sqlc.arg('next_attempt_at'),
    delivered_at = sqlc.narg('delivered_at')
WHERE id = sqlc.arg('id');

-- name: LockInstallWebhooks :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

-- name: UpsertInstallWebhook :exec
INSERT INTO webhooks (kind, url, secret, event_types)
VALUES ('install', sqlc.arg('url'), sqlc.arg('secret'), sqlc.arg('event_types'))
ON CONFLICT (url) WHERE kind = 'install'
DO UPDATE SET secret = EXCLUDED.secret, event_types = EXCLUDED.event_types;

-- name: DeleteInstallWebhooksExcept :execrows
DELETE FROM webhooks
WHERE kind = 'install' AND NOT (url = ANY(sqlc.arg('urls')::text[]));
