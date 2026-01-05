-- name: CreateWorkspace :one
INSERT INTO workspaces (org_id, name, description)
VALUES ($1, $2, $3)
RETURNING id;

-- name: GetWorkspaceByIDQuery :one
SELECT * FROM workspaces WHERE id = $1;

-- name: GetOrganizationIDByWorkspaceID :one
SELECT org_id FROM workspaces WHERE id = $1;

-- name: ListWorkspacesForUser :many
SELECT DISTINCT w.*
FROM workspaces w
JOIN workspace_members wm ON wm.workspace_id = w.id
WHERE wm.user_id = $1
  AND (sqlc.narg('page_token')::text IS NULL
       OR (w.created_at, w.id) < (
         (SELECT created_at FROM workspaces WHERE id = sqlc.narg('page_token')::bigint),
         sqlc.narg('page_token')::bigint
       ))
ORDER BY w.created_at DESC, w.id DESC
LIMIT $2;

-- name: ListWorkspacesInOrg :many
SELECT w.* FROM workspaces w
WHERE w.org_id = $1
  AND (sqlc.narg('page_token')::text IS NULL
       OR (w.created_at, w.id) < (
         (SELECT created_at FROM workspaces WHERE id = sqlc.narg('page_token')::bigint),
         sqlc.narg('page_token')::bigint
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

-- name: UpsertWorkspaceMember :one
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, user_id)
DO UPDATE SET role = EXCLUDED.role
RETURNING user_id;

-- name: GetWorkspaceMemberRole :one
SELECT role FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2;

-- name: IsWorkspaceMember :one
SELECT EXISTS(
  SELECT 1 FROM workspace_members
  WHERE workspace_id = $1 AND user_id = $2
) as is_member;

-- name: DeleteWorkspaceMember :exec
DELETE FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2;

-- name: GetWorkspaceMembers :many
SELECT workspace_id, user_id, role, created_at
FROM workspace_members
WHERE workspace_id = $1;

-- name: ListWorkspaceMembersWithUserDetails :many
SELECT wm.workspace_id, wm.user_id, wm.role, wm.created_at,
       u.name, u.email, u.avatar_url
FROM workspace_members wm
JOIN users u ON wm.user_id = u.id
WHERE wm.workspace_id = $1
  AND (sqlc.narg('page_token')::text IS NULL
       OR (wm.created_at, wm.user_id) < (
         (SELECT created_at FROM workspace_members WHERE workspace_id = $1 AND user_id = sqlc.narg('page_token')::bigint),
         sqlc.narg('page_token')::bigint
       ))
ORDER BY wm.created_at DESC, wm.user_id DESC
LIMIT $2;


-- name: GetWorkspaceOrgID :one
SELECT org_id FROM workspaces WHERE id = $1;
