-- name: InsertEvent :one
INSERT INTO events (type, org_id, workspace_id, actor_type, actor_id, subject_type, subject_id, request_id, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING seq;

-- name: ListOrgEvents :many
SELECT sqlc.embed(e), u.email AS actor_email, u.name AS actor_name
FROM events e
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
WHERE e.org_id = sqlc.arg('org_id')
  AND (sqlc.narg('before_seq')::bigint IS NULL OR e.seq < sqlc.narg('before_seq')::bigint)
  AND (cardinality(sqlc.arg('types')::text[]) = 0 OR e.type = ANY(sqlc.arg('types')::text[]))
ORDER BY e.seq DESC
LIMIT sqlc.arg('max_rows');

-- name: ListEventsAfter :many
SELECT *
FROM events
WHERE (txid, seq) > (sqlc.arg('after_txid')::xid8, sqlc.arg('after_seq')::bigint)
  AND txid < pg_snapshot_xmin(pg_current_snapshot())
ORDER BY txid, seq
LIMIT sqlc.arg('max_rows');

-- name: DeleteEventsBefore :execrows
DELETE FROM events WHERE created_at < $1;
