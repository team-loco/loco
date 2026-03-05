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
         (SELECT created_at FROM users WHERE id = sqlc.narg('page_token')::uuid),
         sqlc.narg('page_token')::uuid
       ))
ORDER BY created_at DESC, id DESC
LIMIT $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CheckUserHasOrganizations :one
SELECT EXISTS(SELECT 1 FROM organizations WHERE created_by = $1) AS has_orgs;

-- name: CheckUserHasWorkspaces :one
SELECT EXISTS(
  SELECT 1 FROM user_scopes
  WHERE user_id = $1 AND entity_type = 'workspace'
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

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = $1;

-- name: DeleteWorkspace :exec
DELETE FROM workspaces WHERE id = $1;
