-- User queries for sqlc

-- name: CreateUser :one
INSERT INTO users (external_id, email, name, avatar_url)
VALUES ($1, $2, $3, $4)
RETURNING id, external_id, email, name, avatar_url, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, external_id, email, name, avatar_url, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, external_id, email, name, avatar_url, created_at, updated_at
FROM users
WHERE email = $1;

-- name: GetUserByExternalID :one
SELECT id, external_id, email, name, avatar_url, created_at, updated_at
FROM users
WHERE external_id = $1;

-- name: UpdateUserAvatarURL :one
UPDATE users
SET avatar_url = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, external_id, email, name, avatar_url, created_at, updated_at;

-- name: ListUsers :many
SELECT id, external_id, email, name, avatar_url, created_at, updated_at
FROM users
WHERE (sqlc.narg('page_token')::text IS NULL
       OR (created_at, id) < (
         (SELECT created_at FROM users WHERE id = sqlc.narg('page_token')::bigint),
         sqlc.narg('page_token')::bigint
       ))
ORDER BY created_at DESC, id DESC
LIMIT $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CheckUserHasOrganizations :one
SELECT EXISTS(SELECT 1 FROM organizations WHERE created_by = $1) AS has_orgs;

-- name: CheckUserHasWorkspaces :one
SELECT EXISTS(
  SELECT 1 FROM workspace_members
  WHERE user_id = $1
) AS has_workspaces;

-- Organization queries

-- name: CreateOrganization :one
INSERT INTO organizations (name, created_by)
VALUES ($1, $2)
RETURNING id, name, created_by, created_at, updated_at;

-- name: GetOrganizationByID :one
SELECT id, name, created_by, created_at, updated_at
FROM organizations
WHERE id = $1;

-- name: GetOrganizationByName :one
SELECT id, name, created_by, created_at, updated_at
FROM organizations
WHERE name = $1;

-- name: IsOrganizationNameUnique :one
SELECT COUNT(*) = 0 AS is_unique
FROM organizations
WHERE name = $1;

-- Organization members queries

-- name: AddOrganizationMember :one
INSERT INTO organization_members (organization_id, user_id)
VALUES ($1, $2)
RETURNING organization_id, user_id;

-- name: GetOrganizationMember :one
SELECT organization_id, user_id
FROM organization_members
WHERE organization_id = $1 AND user_id = $2;

-- name: ListOrganizationMembers :many
SELECT organization_id, user_id
FROM organization_members
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: RemoveOrganizationMember :exec
DELETE FROM organization_members
WHERE organization_id = $1 AND user_id = $2;

-- Workspace members queries

-- name: AddWorkspaceMember :one
INSERT INTO workspace_members (workspace_id, user_id)
VALUES ($1, $2)
RETURNING workspace_id, user_id;

-- name: GetWorkspaceMember :one
SELECT workspace_id, user_id
FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2;

-- name: ListWorkspaceMembers :many
SELECT workspace_id, user_id
FROM workspace_members
WHERE workspace_id = $1
ORDER BY created_at DESC;

-- name: RemoveWorkspaceMember :exec
DELETE FROM workspace_members
WHERE workspace_id = $1 AND user_id = $2;

-- name: ListUserWorkspaces :many
SELECT DISTINCT w.id, w.org_id, w.name, w.description, w.created_by, w.created_at, w.updated_at
FROM workspaces w
JOIN workspace_members wm ON wm.workspace_id = w.id
WHERE wm.user_id = $1
ORDER BY w.created_at DESC;

-- name: ListUserOrganizations :many
SELECT DISTINCT o.id, o.name, o.created_by, o.created_at, o.updated_at
FROM organizations o
JOIN organization_members om ON om.organization_id = o.id
WHERE om.user_id = $1
ORDER BY o.created_at DESC;

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = $1;

-- name: DeleteWorkspace :exec
DELETE FROM workspaces WHERE id = $1;
