-- name: InsertEvent :one
INSERT INTO events (type, org_id, workspace_id, resource_id, environment_id, actor_type, actor_id, subject_type, subject_id, request_id, traceparent, data)
VALUES ($1, $2, $3, $4, $12, $5, $6, $7, $8, $9, $10, $11)
RETURNING seq;

-- name: ListEventsAfter :many
SELECT *
FROM events
WHERE (txid, seq) > (sqlc.arg('after_txid')::xid8, sqlc.arg('after_seq')::bigint)
  AND txid < pg_snapshot_xmin(pg_current_snapshot())
ORDER BY txid, seq
LIMIT sqlc.arg('max_rows');

-- name: DeleteEventsBefore :execrows
DELETE FROM events
WHERE seq IN (
    SELECT e.seq FROM events e
    WHERE e.created_at < sqlc.arg('cutoff')
    LIMIT sqlc.arg('batch_size')
);
