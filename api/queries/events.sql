-- name: InsertEvent :one
INSERT INTO events (type, org_id, workspace_id, resource_id, environment_id, actor_type, actor_id, subject_type, subject_id, request_id, traceparent, data)
VALUES ($1, $2, $3, $4, $12, $5, $6, $7, $8, $9, $10, $11)
RETURNING seq;

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
