-- name: GetUserScopes :many
SELECT ROW(scope, entity_type, entity_id)::entity_scope
FROM user_scopes
WHERE user_id = $1;

-- name: GetUserWithScopesByEmail :one
SELECT * FROM user_with_scopes_view WHERE email = $1;

-- what scopes does user x have on entity y?
-- name: GetUserScopesOnEntity :many
SELECT ROW(scope, entity_type, entity_id)::entity_scope
FROM user_scopes WHERE user_id = $1 AND entity_type = $2 AND entity_id = $3;

-- name: GetUserScopesOnOrganization :many
WITH RECURSIVE entity_hierarchy AS (
    -- Base case: the organization itself
    SELECT
        'organization'::entity_type as entity_type,
        o.id as entity_id,
        o.name as entity_name
    FROM organizations o
    WHERE o.id = $1

    UNION ALL

    -- Workspaces in the organization
    SELECT
        'workspace'::entity_type,
        w.id,
        w.name
    FROM workspaces w
    INNER JOIN entity_hierarchy eh ON eh.entity_type = 'organization' AND eh.entity_id = w.org_id

    UNION ALL

    -- Resources in the workspaces
    SELECT
        'resource'::entity_type,
        r.id,
        r.name
    FROM resources r
    INNER JOIN entity_hierarchy eh ON eh.entity_type = 'workspace' AND eh.entity_id = r.workspace_id
)
SELECT DISTINCT ON (us.entity_type, us.entity_id, us.scope)
    ROW(us.scope, us.entity_type, us.entity_id)::entity_scope
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
    ROW(us.scope, us.entity_type, us.entity_id)::entity_scope
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

-- name: RotateSessionToken :exec
UPDATE session_tokens
SET access_token_hash = $2, refresh_token_hash = $3, access_expires_at = $4, refresh_expires_at = $5, last_used_at = NOW()
WHERE id = $1;

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
