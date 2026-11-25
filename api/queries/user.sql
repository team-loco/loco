-- User queries for sqlc

-- name: CreateUser :one
INSERT INTO users (email, name, avatar_url)
VALUES ($1, $2, $3)
RETURNING id, email, name, avatar_url, status, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, email, name, avatar_url, status, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, email, name, avatar_url, status, created_at, updated_at
FROM users
WHERE email = $1;

-- name: MoveAccountEmail :one
UPDATE users u
SET email = sqlc.arg('email'), updated_at = NOW()
WHERE u.id = sqlc.arg('user_id')
    AND u.email <> sqlc.arg('email')
RETURNING id, email, name, avatar_url, status, created_at, updated_at;

-- name: UpdateUserProfile :one
UPDATE users
SET name = COALESCE(sqlc.narg('name'), name),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    updated_at = NOW()
WHERE id = sqlc.arg('id')
RETURNING id, email, name, avatar_url, status, created_at, updated_at;

-- name: ListUsers :many
SELECT u.id, u.email, u.name, u.avatar_url, u.status, u.created_at, u.updated_at,
       a.last_used_at AS last_active_at
FROM users u
LEFT JOIN session_tokens a ON a.id = (
  SELECT s.id FROM session_tokens s WHERE s.user_id = u.id ORDER BY s.last_used_at DESC LIMIT 1
)
WHERE (sqlc.arg('query')::text = '' OR u.name ILIKE sqlc.arg('query') OR u.email ILIKE sqlc.arg('query'))
  AND (sqlc.narg('page_token')::text IS NULL
       OR (u.created_at, u.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY u.created_at DESC, u.id DESC
LIMIT $1;

-- name: CountUsers :one
SELECT COUNT(*) FROM (
  SELECT 1 FROM users u
  WHERE sqlc.arg('query')::text = '' OR u.name ILIKE sqlc.arg('query') OR u.email ILIKE sqlc.arg('query')
  LIMIT $1
) capped;

-- name: GetUserLastActive :one
SELECT a.last_used_at AS last_active_at
FROM users u
LEFT JOIN session_tokens a ON a.id = (
  SELECT s.id FROM session_tokens s WHERE s.user_id = u.id ORDER BY s.last_used_at DESC LIMIT 1
)
WHERE u.id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- Organization queries

-- name: CreateOrganization :one
INSERT INTO organizations (name, created_by)
VALUES ($1, $2)
RETURNING id, name, created_by, created_at, updated_at;

-- name: GetOrganizationByID :one
SELECT id, name, created_by, created_at, updated_at
FROM organizations
WHERE id = $1;

-- name: DeleteWorkspace :exec
DELETE FROM workspaces WHERE id = $1;

-- name: SetUserStatus :one
UPDATE users
SET status = $2, updated_at = NOW()
WHERE id = $1
RETURNING id, email, name, avatar_url, status, created_at, updated_at;
