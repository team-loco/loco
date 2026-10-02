-- name: GetUserScopes :many
SELECT entity_type, entity_id, scope
FROM user_scopes
WHERE user_id = $1;

-- what scopes does user x have on entity y?
-- name: GetUserScopesOnEntity :many
SELECT entity_type, entity_id, scope
FROM user_scopes WHERE user_id = $1 AND entity_type = $2 AND entity_id = $3;

-- name: GetUserScopesOnOrganization :many
WITH entity_hierarchy AS (
     SELECT 'organization'::entity_type AS entity_type, o.id AS entity_id
     FROM organizations o
     WHERE o.id = $1

     UNION ALL

     SELECT 'workspace'::entity_type, w.id
     FROM workspaces w
     WHERE w.org_id = $1

     UNION ALL

     SELECT 'resource'::entity_type, r.id
     FROM resources r
     INNER JOIN workspaces w ON w.id = r.workspace_id
     WHERE w.org_id = $1
 )
 SELECT DISTINCT ON (us.entity_type, us.entity_id, us.scope)
     us.entity_type, us.entity_id, us.scope
 FROM user_scopes us
 INNER JOIN entity_hierarchy eh ON us.entity_type = eh.entity_type AND us.entity_id = eh.entity_id
 WHERE us.user_id = $2
 ORDER BY us.entity_type, us.entity_id, us.scope;

-- name: GetUserScopesOnWorkspace :many
WITH RECURSIVE entity_hierarchy AS (
     -- Base case: the workspace itself
     SELECT
         'workspace'::entity_type as entity_type,
         w.id as entity_id,
         w.name as entity_name
     FROM workspaces w
     WHERE w.id = $1

     UNION ALL

     -- Resources in the workspace
     SELECT
         'resource'::entity_type,
         r.id,
         r.name
     FROM resources r
     INNER JOIN entity_hierarchy eh ON eh.entity_type = 'workspace' AND eh.entity_id = r.workspace_id
 )
 SELECT DISTINCT ON (us.entity_type, us.entity_id, us.scope)
     us.entity_type, us.entity_id, us.scope
 FROM user_scopes us
 INNER JOIN entity_hierarchy eh ON us.entity_type = eh.entity_type AND us.entity_id = eh.entity_id
 WHERE us.user_id = $2
 ORDER BY us.entity_type, us.entity_id, us.scope;

 -- what users have scope z on entity y?
-- name: GetUsersWithScopeOnEntity :many
SELECT user_id FROM user_scopes WHERE entity_type = $1 AND entity_id = $2 AND scope = $3;

-- name: AddUserScope :exec
INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING;

-- name: RemoveUserScope :exec
DELETE FROM user_scopes WHERE user_id = $1 AND scope = $2 AND entity_type = $3 AND entity_id = $4;

-- name: RemoveAllScopesForUserOnEntity :exec
DELETE FROM user_scopes WHERE user_id = $1 AND entity_type = $2 AND entity_id = $3;

-- name: RemoveAllScopesForEntity :exec
DELETE FROM user_scopes WHERE entity_type = $1 AND entity_id = $2;

-- name: RemoveAllScopesForUser :exec
DELETE FROM user_scopes WHERE user_id = $1;

-- -----------------------------------------------------------------------------
-- Session token queries
-- -----------------------------------------------------------------------------

-- name: CreateSessionToken :exec
INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id, access_expires_at, refresh_expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetSessionByAccessToken :one
SELECT id, user_id, access_expires_at, refresh_expires_at, last_used_at, ip_address, user_agent, created_at
FROM session_tokens
WHERE access_token_hash = $1 AND access_expires_at > NOW();

-- name: GetSessionByRefreshToken :one
SELECT id, user_id, refresh_token_hash, access_expires_at, refresh_expires_at, last_used_at, ip_address, user_agent, created_at
FROM session_tokens
WHERE refresh_token_hash = $1 AND refresh_expires_at > NOW();

-- name: RotateSessionToken :execrows
UPDATE session_tokens
SET access_token_hash = sqlc.arg('access_token_hash'),
    refresh_token_hash = sqlc.arg('refresh_token_hash'),
    access_expires_at = sqlc.arg('access_expires_at'),
    refresh_expires_at = sqlc.arg('refresh_expires_at'),
    last_used_at = NOW()
WHERE id = sqlc.arg('id') AND refresh_token_hash = sqlc.arg('old_refresh_token_hash');

-- name: TouchSessionLastUsed :exec
UPDATE session_tokens SET last_used_at = NOW() WHERE id = $1;

-- name: DeleteSessionToken :exec
DELETE FROM session_tokens WHERE id = $1;

-- name: DeleteSessionTokenByAccessHash :exec
DELETE FROM session_tokens WHERE access_token_hash = $1;

-- session is fully dead once the refresh token expires (access expiry alone is not enough)
-- name: DeleteExpiredSessionTokens :exec
DELETE FROM session_tokens WHERE refresh_expires_at < NOW();

-- name: ListSessionsForUser :many
SELECT id, access_expires_at, refresh_expires_at, last_used_at, ip_address, user_agent, created_at
FROM session_tokens
WHERE user_id = $1
ORDER BY last_used_at DESC;

-- -----------------------------------------------------------------------------
-- API token queries
-- -----------------------------------------------------------------------------

-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, token_hash, name, entity_type, entity_id, scopes, created_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetAPIToken :one
SELECT id, name, entity_type, entity_id, scopes, created_by, created_at, expires_at, last_used_at
FROM api_tokens
WHERE token_hash = $1 AND expires_at > NOW();

-- name: TouchAPITokenLastUsed :exec
UPDATE api_tokens SET last_used_at = NOW() WHERE id = $1;

-- name: DeleteAPIToken :exec
DELETE FROM api_tokens WHERE id = $1;

-- name: DeleteAPITokenByHash :exec
DELETE FROM api_tokens WHERE token_hash = $1;

-- name: DeleteExpiredAPITokens :exec
DELETE FROM api_tokens WHERE expires_at < NOW();

-- name: ListAPITokensForEntity :many
SELECT id, name, entity_type, entity_id, scopes, created_at, expires_at, last_used_at
FROM api_tokens
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC;

-- name: DeleteAPITokensForEntity :exec
DELETE FROM api_tokens WHERE entity_type = $1 AND entity_id = $2;

-- name: GetAPITokenByNameAndEntity :one
SELECT id, name, entity_type, entity_id, scopes, created_at, expires_at, last_used_at
FROM api_tokens
WHERE name = $1 AND entity_type = $2 AND entity_id = $3;

-- name: DeleteAPITokenByNameAndEntity :exec
DELETE FROM api_tokens WHERE name = $1 AND entity_type = $2 AND entity_id = $3;
