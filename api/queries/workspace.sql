-- name: InsertWorkspace :one
INSERT INTO workspaces (org_id, name, description, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetWorkspaceByIDQuery :one
SELECT * FROM workspaces WHERE id = $1;

-- name: GetOrganizationIDByWorkspaceID :one
SELECT org_id FROM workspaces WHERE id = $1;

-- name: ListWorkspacesForUser :many
SELECT DISTINCT w.*
FROM workspaces w
JOIN workspace_members wm ON wm.workspace_id = w.id
WHERE wm.user_id = $1
ORDER BY w.created_at DESC;

-- name: ListWorkspacesInOrg :many
SELECT * FROM workspaces
WHERE org_id = $1
ORDER BY created_at DESC;

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
RETURNING *;

-- name: RemoveWorkspace :exec
DELETE FROM workspaces WHERE id = $1;

-- name: UpsertWorkspaceMember :one
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, user_id)
DO UPDATE SET role = EXCLUDED.role
RETURNING *;

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

-- TODO: Uncomment when apps table exists
-- -- name: CountAppsInWorkspace :one
-- SELECT COUNT(*) FROM apps WHERE workspace_id = $1;

-- name: GetWorkspaceOrgID :one
SELECT org_id FROM workspaces WHERE id = $1;
