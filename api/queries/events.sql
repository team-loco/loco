-- name: InsertEvent :one
INSERT INTO events (type, org_id, workspace_id, actor_type, actor_id, subject_type, subject_id, request_id, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING seq;

-- name: ListOrgEvents :many
SELECT seq, id, type, org_id, workspace_id, actor_type, actor_id, subject_type, subject_id, request_id, data, created_at
FROM events
WHERE org_id = sqlc.arg('org_id')
  AND (sqlc.narg('before_seq')::bigint IS NULL OR seq < sqlc.narg('before_seq')::bigint)
  AND (cardinality(sqlc.arg('types')::text[]) = 0 OR type = ANY(sqlc.arg('types')::text[]))
ORDER BY seq DESC
LIMIT sqlc.arg('max_rows');

-- name: ListEventsAfter :many
SELECT seq, id, type, org_id, workspace_id, actor_type, actor_id, subject_type, subject_id, request_id, data, created_at
FROM events
WHERE seq > $1
ORDER BY seq
LIMIT $2;

-- name: DeleteEventsBefore :execrows
DELETE FROM events WHERE created_at < $1;
