-- name: InsertEvent :one
INSERT INTO events (type, org_id, workspace_id, resource_id, environment_id, actor_type, actor_id, subject_type, subject_id, request_id, traceparent, data)
VALUES ($1, $2, $3, $4, $12, $5, $6, $7, $8, $9, $10, $11)
RETURNING seq;

-- name: ListOrgAuditEvents :many
SELECT sqlc.embed(e), u.email AS actor_email, u.name AS actor_name
FROM events e
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
WHERE e.org_id = sqlc.arg('org_id')
  AND (sqlc.narg('environment_id')::uuid IS NULL OR e.environment_id = sqlc.narg('environment_id'))
  AND (sqlc.narg('start_time')::timestamptz IS NULL OR e.created_at >= sqlc.narg('start_time'))
  AND (sqlc.narg('end_time')::timestamptz IS NULL OR e.created_at < sqlc.narg('end_time'))
  AND (sqlc.narg('before_seq')::bigint IS NULL OR e.seq < sqlc.narg('before_seq')::bigint)
  AND (cardinality(sqlc.arg('types')::text[]) = 0 OR e.type = ANY(sqlc.arg('types')::text[]))
  AND ((coalesce(cardinality(sqlc.arg('actor_ids')::uuid[]), 0) = 0 AND coalesce(cardinality(sqlc.arg('actor_types')::text[]), 0) = 0)
    OR e.actor_id = ANY(sqlc.arg('actor_ids')::uuid[])
    OR e.actor_type = ANY(sqlc.arg('actor_types')::text[]))
  -- Separate candidate queries let each search index narrow the history before paging.
  AND (sqlc.arg('query')::text = '' OR e.seq = ANY(ARRAY(
  SELECT candidate.seq FROM events candidate
  WHERE candidate.org_id = sqlc.arg('org_id') AND sqlc.arg('query')::text <> ''
    AND (candidate.request_id LIKE sqlc.arg('request_prefix')::text
      OR candidate.data->>'name' ILIKE sqlc.arg('substring')::text
      OR candidate.data->>'domain' ILIKE sqlc.arg('substring')::text)
  UNION
  SELECT a.seq FROM users actor
  JOIN events a ON a.actor_type = 'user' AND a.actor_id = actor.id
  WHERE a.org_id = sqlc.arg('org_id') AND sqlc.arg('query')::text <> ''
    AND (actor.name ILIKE sqlc.arg('substring')::text OR actor.email ILIKE sqlc.arg('substring')::text)
  )))
ORDER BY e.seq DESC
LIMIT sqlc.arg('max_rows');

-- name: ListWorkspaceAuditEvents :many
SELECT sqlc.embed(e), u.email AS actor_email, u.name AS actor_name
FROM events e
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
WHERE e.org_id = sqlc.arg('org_id')
  AND e.workspace_id = sqlc.arg('workspace_id')
  AND (sqlc.narg('environment_id')::uuid IS NULL OR e.environment_id = sqlc.narg('environment_id'))
  AND (sqlc.narg('start_time')::timestamptz IS NULL OR e.created_at >= sqlc.narg('start_time'))
  AND (sqlc.narg('end_time')::timestamptz IS NULL OR e.created_at < sqlc.narg('end_time'))
  AND (sqlc.narg('before_seq')::bigint IS NULL OR e.seq < sqlc.narg('before_seq')::bigint)
  AND (cardinality(sqlc.arg('types')::text[]) = 0 OR e.type = ANY(sqlc.arg('types')::text[]))
  AND ((coalesce(cardinality(sqlc.arg('actor_ids')::uuid[]), 0) = 0 AND coalesce(cardinality(sqlc.arg('actor_types')::text[]), 0) = 0)
    OR e.actor_id = ANY(sqlc.arg('actor_ids')::uuid[])
    OR e.actor_type = ANY(sqlc.arg('actor_types')::text[]))
  AND (sqlc.arg('query')::text = '' OR e.seq = ANY(ARRAY(
  SELECT candidate.seq FROM events candidate
  WHERE candidate.org_id = sqlc.arg('org_id') AND sqlc.arg('query')::text <> ''
    AND (candidate.request_id LIKE sqlc.arg('request_prefix')::text
      OR candidate.data->>'name' ILIKE sqlc.arg('substring')::text
      OR candidate.data->>'domain' ILIKE sqlc.arg('substring')::text)
  UNION
  SELECT a.seq FROM users actor
  JOIN events a ON a.actor_type = 'user' AND a.actor_id = actor.id
  WHERE a.org_id = sqlc.arg('org_id') AND sqlc.arg('query')::text <> ''
    AND (actor.name ILIKE sqlc.arg('substring')::text OR actor.email ILIKE sqlc.arg('substring')::text)
  )))
ORDER BY e.seq DESC
LIMIT sqlc.arg('max_rows');

-- name: ListResourceAuditEvents :many
SELECT sqlc.embed(e), u.email AS actor_email, u.name AS actor_name
FROM events e
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
WHERE e.org_id = sqlc.arg('org_id')
  AND e.workspace_id = sqlc.arg('workspace_id')
  AND e.resource_id = sqlc.arg('resource_id')
  AND (sqlc.narg('environment_id')::uuid IS NULL OR e.environment_id = sqlc.narg('environment_id'))
  AND (sqlc.narg('start_time')::timestamptz IS NULL OR e.created_at >= sqlc.narg('start_time'))
  AND (sqlc.narg('end_time')::timestamptz IS NULL OR e.created_at < sqlc.narg('end_time'))
  AND (sqlc.narg('before_seq')::bigint IS NULL OR e.seq < sqlc.narg('before_seq')::bigint)
  AND (cardinality(sqlc.arg('types')::text[]) = 0 OR e.type = ANY(sqlc.arg('types')::text[]))
  AND ((coalesce(cardinality(sqlc.arg('actor_ids')::uuid[]), 0) = 0 AND coalesce(cardinality(sqlc.arg('actor_types')::text[]), 0) = 0)
    OR e.actor_id = ANY(sqlc.arg('actor_ids')::uuid[])
    OR e.actor_type = ANY(sqlc.arg('actor_types')::text[]))
  AND (sqlc.arg('query')::text = '' OR e.seq = ANY(ARRAY(
  SELECT candidate.seq FROM events candidate
  WHERE candidate.org_id = sqlc.arg('org_id') AND sqlc.arg('query')::text <> ''
    AND (candidate.request_id LIKE sqlc.arg('request_prefix')::text
      OR candidate.data->>'name' ILIKE sqlc.arg('substring')::text
      OR candidate.data->>'domain' ILIKE sqlc.arg('substring')::text)
  UNION
  SELECT a.seq FROM users actor
  JOIN events a ON a.actor_type = 'user' AND a.actor_id = actor.id
  WHERE a.org_id = sqlc.arg('org_id') AND sqlc.arg('query')::text <> ''
    AND (actor.name ILIKE sqlc.arg('substring')::text OR actor.email ILIKE sqlc.arg('substring')::text)
  )))
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
