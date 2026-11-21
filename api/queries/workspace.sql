-- name: CreateWorkspace :one
INSERT INTO workspaces (org_id, name, description, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: GetWorkspaceByIDQuery :one
SELECT w.id, w.org_id, o.name AS org_name, w.name, w.description, w.created_by, w.created_at, w.updated_at,
       w.log_retention_days, w.trace_retention_days, w.metric_retention_days
FROM workspaces w
JOIN organizations o ON o.id = w.org_id
WHERE w.id = $1;

-- name: GetOrganizationIDByWorkspaceID :one
SELECT org_id FROM workspaces WHERE id = $1;

-- name: ListWorkspacesForUser :many
SELECT v.id, v.org_id, o.name AS org_name, v.name, v.description, v.created_by, v.created_at, v.updated_at,
       v.log_retention_days, v.trace_retention_days, v.metric_retention_days
FROM (
    SELECT w.id, w.org_id, w.name, w.description, w.created_by, w.created_at, w.updated_at,
           w.log_retention_days, w.trace_retention_days, w.metric_retention_days
    FROM user_scopes us
    JOIN workspaces w ON w.id = us.entity_id
    WHERE us.user_id = $1 AND us.scope = 'read' AND us.entity_type = 'workspace'
      AND (sqlc.narg('page_token')::text IS NULL
           OR (w.created_at, w.id) < (
             sqlc.narg('page_created_at')::timestamptz,
             sqlc.narg('page_token')::uuid
           ))
    UNION
    SELECT ow.id, ow.org_id, ow.name, ow.description, ow.created_by, ow.created_at, ow.updated_at,
           ow.log_retention_days, ow.trace_retention_days, ow.metric_retention_days
    FROM user_scopes us
    CROSS JOIN LATERAL (
        SELECT w.id, w.org_id, w.name, w.description, w.created_by, w.created_at, w.updated_at,
               w.log_retention_days, w.trace_retention_days, w.metric_retention_days
        FROM workspaces w
        WHERE w.org_id = us.entity_id
          AND (sqlc.narg('page_token')::text IS NULL
               OR (w.created_at, w.id) < (
                 sqlc.narg('page_created_at')::timestamptz,
                 sqlc.narg('page_token')::uuid
               ))
        ORDER BY w.created_at DESC, w.id DESC
        LIMIT $2
    ) ow
    WHERE us.user_id = $1 AND us.scope = 'read' AND us.entity_type = 'organization'
) v
JOIN organizations o ON o.id = v.org_id
ORDER BY v.created_at DESC, v.id DESC
LIMIT $2;

-- name: CountWorkspacesForUser :one
SELECT COUNT(*) FROM (
  SELECT w.id FROM user_scopes us
  JOIN workspaces w ON w.id = us.entity_id
  WHERE us.user_id = $1 AND us.scope = 'read' AND us.entity_type = 'workspace'
  UNION
  SELECT w.id FROM user_scopes us
  JOIN workspaces w ON w.org_id = us.entity_id
  WHERE us.user_id = $1 AND us.scope = 'read' AND us.entity_type = 'organization'
  LIMIT $2
) capped;

-- name: ListWorkspacesInOrg :many
SELECT w.id, w.org_id, o.name AS org_name, w.name, w.description, w.created_by, w.created_at, w.updated_at,
       w.log_retention_days, w.trace_retention_days, w.metric_retention_days
FROM workspaces w
JOIN organizations o ON o.id = w.org_id
WHERE w.org_id = $1
  AND (sqlc.arg('all_workspaces')::bool
       OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
       OR w.id IN (SELECT r.workspace_id FROM resources r WHERE r.id = ANY(sqlc.arg('resource_ids')::uuid[])))
  AND (sqlc.arg('query')::text = '' OR w.name ILIKE sqlc.arg('query')
       OR ((sqlc.arg('all_workspaces')::bool OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[]))
           AND w.description ILIKE sqlc.arg('query')))
  AND (NOT sqlc.arg('has_resources')::bool OR EXISTS (SELECT 1 FROM resources r WHERE r.workspace_id = w.id))
  AND (sqlc.narg('page_token')::text IS NULL
       OR (w.created_at, w.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY w.created_at DESC, w.id DESC
LIMIT $2;

-- name: CountWorkspacesInOrg :one
SELECT COUNT(*) FROM (
  SELECT 1 FROM workspaces w
  WHERE w.org_id = $1
    AND (sqlc.arg('all_workspaces')::bool
         OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[])
         OR w.id IN (SELECT r.workspace_id FROM resources r WHERE r.id = ANY(sqlc.arg('resource_ids')::uuid[])))
    AND (sqlc.arg('query')::text = '' OR w.name ILIKE sqlc.arg('query')
         OR ((sqlc.arg('all_workspaces')::bool OR w.id = ANY(sqlc.arg('workspace_ids')::uuid[]))
             AND w.description ILIKE sqlc.arg('query')))
    AND (NOT sqlc.arg('has_resources')::bool OR EXISTS (SELECT 1 FROM resources r WHERE r.workspace_id = w.id))
  LIMIT $2
) capped;

-- name: CountWorkspaceContents :many
SELECT w.id,
       (SELECT COUNT(*) FROM resources r WHERE r.workspace_id = w.id)::int AS resource_count,
       (SELECT COUNT(*) FROM environments e WHERE e.workspace_id = w.id)::int AS environment_count,
       (SELECT COUNT(*) FROM webhooks wh WHERE wh.kind = 'workspace' AND wh.workspace_id = w.id)::int AS webhook_count,
       (SELECT COUNT(DISTINCT us.user_id) FROM user_scopes us
        WHERE us.entity_type = 'workspace' AND us.entity_id = w.id)::int AS member_count
FROM workspaces w
WHERE w.id = ANY(sqlc.arg('workspace_ids')::uuid[]);

-- name: UpdateWorkspace :exec
UPDATE workspaces
SET name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    log_retention_days = sqlc.narg('log_retention_days'),
    trace_retention_days = sqlc.narg('trace_retention_days'),
    metric_retention_days = sqlc.narg('metric_retention_days'),
    updated_at = NOW()
WHERE id = $1;

-- name: RemoveWorkspace :exec
DELETE FROM workspaces WHERE id = $1;

-- name: LockWorkspaceForDeletion :one
SELECT org_id, name FROM workspaces WHERE id = $1 FOR UPDATE;

-- name: ListWorkspaceResourcesForDeletion :many
SELECT id, name FROM resources WHERE workspace_id = $1 ORDER BY id FOR UPDATE;

-- name: ListWorkspaceMembersWithUserDetails :many
WITH member_scopes AS (
  SELECT
    us.entity_id AS workspace_id,
    us.user_id,
    array_agg(us.scope ORDER BY us.scope)::text[] AS scopes,
    MIN(us.created_at)::timestamptz AS joined_at
  FROM user_scopes us
  WHERE us.entity_id = $1 AND us.entity_type = 'workspace'
  GROUP BY us.entity_id, us.user_id
)
SELECT ms.workspace_id, ms.user_id, ms.scopes, ms.joined_at,
       u.name, u.email, u.avatar_url
FROM member_scopes ms
JOIN users u ON u.id = ms.user_id
WHERE (sqlc.narg('page_token')::text IS NULL
       OR (ms.joined_at, ms.user_id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY ms.joined_at DESC, ms.user_id DESC
LIMIT $2;

-- name: LockWorkspace :one
SELECT org_id, name, description, log_retention_days, trace_retention_days, metric_retention_days
FROM workspaces WHERE id = $1 FOR UPDATE;

-- name: ListWorkspaceRetention :many
SELECT id, org_id, log_retention_days, trace_retention_days, metric_retention_days
FROM workspaces WHERE id = ANY(sqlc.arg('ids')::uuid[]);
