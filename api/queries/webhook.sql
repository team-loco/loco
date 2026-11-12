-- name: CreateWorkspaceWebhook :one
INSERT INTO webhooks (id, kind, workspace_id, url, provider, kek_id, wrapped_dek, secret, event_types, created_by)
VALUES (
    sqlc.arg('id'), 'workspace', sqlc.arg('workspace_id')::uuid, sqlc.arg('url'),
    sqlc.arg('provider'), sqlc.arg('kek_id'), sqlc.arg('wrapped_dek'), sqlc.arg('secret'),
    sqlc.arg('event_types'), sqlc.narg('created_by')
)
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

-- name: UpdateWorkspaceWebhook :one
UPDATE webhooks
SET url = sqlc.arg('url'), event_types = sqlc.arg('event_types')
WHERE id = sqlc.arg('id') AND kind = 'workspace' AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: RotateWorkspaceWebhookSecret :one
UPDATE webhooks
SET previous_secret = CASE WHEN sqlc.arg('grace_seconds')::int > 0 THEN secret END,
    previous_secret_expires_at = CASE
        WHEN sqlc.arg('grace_seconds')::int > 0 THEN NOW() + make_interval(secs => sqlc.arg('grace_seconds')::int)
    END,
    secret = sqlc.arg('secret')
WHERE id = sqlc.arg('id') AND kind = 'workspace' AND workspace_id = sqlc.arg('workspace_id')::uuid
RETURNING *;

-- name: DeleteWorkspaceWebhook :execrows
DELETE FROM webhooks
WHERE id = sqlc.arg('id') AND kind = 'workspace' AND workspace_id = sqlc.arg('workspace_id')::uuid;

-- name: ListWebhookDeliveries :many
SELECT d.id, d.status, d.attempts, d.next_attempt_at, d.last_error, d.created_at, d.delivered_at,
       e.type AS event_type, e.id AS event_id
FROM webhook_deliveries d
JOIN events e ON e.seq = d.event_seq
WHERE d.webhook_id = sqlc.arg('webhook_id')
  AND (sqlc.arg('status')::text = '' OR d.status = sqlc.arg('status'))
  AND (sqlc.narg('page_token')::text IS NULL
       OR (d.created_at, d.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY d.created_at DESC, d.id DESC
LIMIT sqlc.arg('max_rows');

-- name: LockWebhookDeliveryStatus :one
SELECT status FROM webhook_deliveries
WHERE id = sqlc.arg('id') AND webhook_id = sqlc.arg('webhook_id')
FOR UPDATE;

-- name: RequeueWebhookDelivery :exec
UPDATE webhook_deliveries
SET status = 'pending', attempts = 0, next_attempt_at = NOW(), delivered_at = NULL, last_error = NULL
WHERE id = $1;

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
SELECT d.id, w.id AS webhook_id, w.url, w.provider, w.kek_id, w.wrapped_dek,
       w.secret, w.previous_secret, w.previous_secret_expires_at, w.kind,
       e.seq, e.id AS event_id, e.type, e.org_id, e.workspace_id, e.resource_id, e.environment_id, e.actor_type, e.actor_id,
       e.subject_type, e.subject_id, e.request_id, e.traceparent, e.data, e.created_at
FROM webhook_deliveries d
JOIN webhooks w ON w.id = d.webhook_id
JOIN events e ON e.seq = d.event_seq
WHERE d.id = $1;

-- name: FinishWebhookDelivery :execrows
UPDATE webhook_deliveries
SET status = sqlc.arg('status'),
    last_error = sqlc.narg('last_error'),
    next_attempt_at = sqlc.arg('next_attempt_at'),
    delivered_at = sqlc.narg('delivered_at')
WHERE id = sqlc.arg('id') AND attempts = sqlc.arg('attempts');

-- name: PostponeWebhookDelivery :execrows
UPDATE webhook_deliveries
SET attempts = attempts - 1,
    last_error = sqlc.arg('last_error'),
    next_attempt_at = sqlc.arg('next_attempt_at')
WHERE id = sqlc.arg('id') AND attempts = sqlc.arg('attempts');

-- name: LockInstallWebhooks :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

-- name: ListInstallWebhookIDs :many
SELECT id, url FROM webhooks WHERE kind = 'install';

-- name: UpsertInstallWebhook :exec
INSERT INTO webhooks (id, kind, url, provider, kek_id, wrapped_dek, secret, event_types)
VALUES (
    sqlc.arg('id'), 'install', sqlc.arg('url'),
    sqlc.arg('provider'), sqlc.arg('kek_id'), sqlc.arg('wrapped_dek'), sqlc.arg('secret'),
    sqlc.arg('event_types')
)
ON CONFLICT (url) WHERE kind = 'install'
DO UPDATE SET provider = EXCLUDED.provider,
              kek_id = EXCLUDED.kek_id,
              wrapped_dek = EXCLUDED.wrapped_dek,
              secret = EXCLUDED.secret,
              event_types = EXCLUDED.event_types;

-- name: DeleteInstallWebhooksExcept :execrows
DELETE FROM webhooks
WHERE kind = 'install' AND NOT (url = ANY(sqlc.arg('urls')::text[]));
