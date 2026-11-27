-- name: InsertEvent :one
INSERT INTO events (type, org_id, workspace_id, resource_id, environment_id, deployment_id, actor_type, actor_id, subject_type, subject_id, request_id, traceparent, data,
                    actor_name, actor_email, actor_token_type, acted_as_operator)
SELECT $1, $2, $3, $4, $12, $13, $5, $6, $7, $8, $9, $10, $11,
       CASE WHEN sqlc.arg(has_actor_snapshot)::boolean THEN sqlc.narg(actor_name)::text ELSE COALESCE(u.name, t.name) END,
       CASE WHEN sqlc.arg(has_actor_snapshot)::boolean THEN sqlc.narg(actor_email)::text ELSE u.email END,
       CASE WHEN sqlc.arg(has_actor_snapshot)::boolean THEN sqlc.narg(actor_token_type)::text ELSE t.entity_type::text END,
       sqlc.arg(acted_as_operator)::boolean
FROM (SELECT 1) one
LEFT JOIN users u ON $5 = 'user' AND u.id = $6
LEFT JOIN api_tokens t ON $5 = 'api_token' AND t.id = $6
RETURNING seq;

-- name: GetEventActor :one
SELECT COALESCE(u.name, t.name, '')::text AS name, u.email, COALESCE(t.entity_type::text, '')::text AS token_type
FROM (SELECT 1) one
LEFT JOIN users u ON sqlc.arg(actor_type)::text = 'user' AND u.id = sqlc.narg(actor_id)::uuid
LEFT JOIN api_tokens t ON sqlc.arg(actor_type)::text = 'api_token' AND t.id = sqlc.narg(actor_id)::uuid;

-- name: ListEventsAfter :many
SELECT *
FROM events
WHERE stream_position > sqlc.arg('after_position')::bigint
ORDER BY stream_position
LIMIT sqlc.arg('max_rows');

-- name: DeleteEventsBefore :execrows
DELETE FROM events
WHERE seq IN (
    SELECT e.seq FROM events e
    WHERE e.created_at < sqlc.arg('cutoff') AND e.stream_position IS NOT NULL
    LIMIT sqlc.arg('batch_size')
);

-- name: LockEventStreamCounter :one
SELECT position FROM event_stream_counter WHERE singleton FOR UPDATE;

-- name: PublishEvents :one
WITH batch AS (
    SELECT seq, sqlc.arg('position')::bigint + row_number() OVER (ORDER BY seq) AS position
    FROM (SELECT seq FROM events WHERE stream_position IS NULL ORDER BY seq LIMIT sqlc.arg('batch_size')) unpublished
), published AS (
    UPDATE events SET stream_position = batch.position FROM batch WHERE events.seq = batch.seq
    RETURNING events.stream_position
)
UPDATE event_stream_counter
SET position = COALESCE((SELECT max(stream_position) FROM published), position)
WHERE singleton
RETURNING position;
