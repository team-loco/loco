-- name: CreateWorkspace :one
INSERT INTO workspaces (org_id, name, description, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: GetWorkspaceByIDQuery :one
SELECT * FROM workspaces WHERE id = $1;

-- name: GetOrganizationIDByWorkspaceID :one
SELECT org_id FROM workspaces WHERE id = $1;

-- name: ListWorkspacesForUser :many
SELECT DISTINCT w.id, w.org_id, w.name, w.description, w.created_by, w.created_at, w.updated_at
FROM workspaces w
JOIN user_scopes us ON us.entity_id = w.id
  AND us.entity_type = 'workspace'
  AND us.user_id = $1
WHERE (sqlc.narg('page_token')::text IS NULL
       OR (w.created_at, w.id) < (
         (SELECT created_at FROM workspaces WHERE id = sqlc.narg('page_token')::uuid),
         sqlc.narg('page_token')::uuid
       ))
ORDER BY w.created_at DESC, w.id DESC
LIMIT $2;

-- name: ListWorkspacesInOrg :many
SELECT w.* FROM workspaces w
WHERE w.org_id = $1
  AND (sqlc.narg('page_token')::text IS NULL
       OR (w.created_at, w.id) < (
         (SELECT created_at FROM workspaces WHERE id = sqlc.narg('page_token')::uuid),
         sqlc.narg('page_token')::uuid
       ))
ORDER BY w.created_at DESC, w.id DESC
LIMIT $2;

-- name: IsWorkspaceNameUniqueInOrg :one
SELECT COUNT(*) = 0 as is_unique
FROM workspaces
WHERE org_id = $1
AND name = $2;

-- name: UpdateWorkspace :one
UPDATE workspaces
SET name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    updated_at = NOW()
WHERE id = $1
RETURNING id;

-- name: RemoveWorkspace :exec
DELETE FROM workspaces WHERE id = $1;

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
       OR ms.user_id < sqlc.narg('page_token')::uuid)
ORDER BY ms.user_id DESC
LIMIT $2;

-- name: GetWorkspaceOrgID :one
SELECT org_id FROM workspaces WHERE id = $1;
