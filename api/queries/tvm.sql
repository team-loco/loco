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
INSERT INTO user_scopes (user_id, scope, entity_type, entity_id, created_at)
VALUES ($1, $2, $3, $4,
  CASE WHEN $3::entity_type = 'organization' THEN
    COALESCE((SELECT MIN(created_at) FROM user_scopes
              WHERE user_id = $1 AND entity_type = $3 AND entity_id = $4), NOW())
  ELSE NOW() END)
ON CONFLICT DO NOTHING;

-- name: RemoveUserScope :exec
DELETE FROM user_scopes WHERE user_id = $1 AND scope = $2 AND entity_type = $3 AND entity_id = $4;

-- name: RemoveAllScopesForUserOnEntity :exec
DELETE FROM user_scopes WHERE user_id = $1 AND entity_type = $2 AND entity_id = $3;

-- name: RemoveResourceScopesForUserInWorkspace :exec
DELETE FROM user_scopes
WHERE user_id = $1
  AND entity_type = 'resource'
  AND entity_id IN (SELECT id FROM resources WHERE workspace_id = $2);

-- name: RemoveAllScopesForEntity :exec
DELETE FROM user_scopes WHERE entity_type = $1 AND entity_id = $2;

-- name: RemoveAllScopesForUser :exec
DELETE FROM user_scopes WHERE user_id = $1;

-- -----------------------------------------------------------------------------
-- Session token queries
-- -----------------------------------------------------------------------------

-- name: CreateSessionToken :exec
INSERT INTO session_tokens (id, access_token_hash, refresh_token_hash, user_id, access_expires_at, refresh_expires_at, ip_address, user_agent, identity_id, kind)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetSessionWithScopesByAccessToken :one
SELECT
    st.id,
    st.user_id,
    st.kind,
    st.identity_id,
    st.last_used_at,
    COALESCE(
        (
            SELECT JSON_AGG(
                JSON_BUILD_OBJECT(
                    'scope', us.scope,
                    'entity_type', us.entity_type,
                    'entity_id', us.entity_id
                )
            )
            FROM user_scopes us
            WHERE us.user_id = st.user_id
        ),
        '[]'::json
    )::json AS scopes
FROM session_tokens st
JOIN users u ON u.id = st.user_id
WHERE st.access_token_hash = $1 AND st.access_expires_at > NOW() AND u.status = 'active';

-- name: GetSessionByRefreshToken :one
SELECT st.id, st.user_id, st.kind, false AS used
FROM session_tokens st
WHERE st.refresh_token_hash = $1 AND st.refresh_expires_at > NOW()
UNION ALL
SELECT st.id, st.user_id, st.kind, true AS used
FROM used_session_refresh_tokens r
JOIN session_tokens st ON st.id = r.session_id
WHERE r.token_hash = $1 AND r.expires_at > NOW();

-- name: RotateSessionToken :execrows
WITH rotated AS (
    UPDATE session_tokens AS st
    SET access_token_hash = sqlc.arg('access_token_hash'),
        refresh_token_hash = sqlc.arg('refresh_token_hash'),
        access_expires_at = sqlc.arg('access_expires_at'),
        refresh_expires_at = sqlc.arg('refresh_expires_at'),
        last_used_at = NOW()
    FROM session_tokens AS previous
    WHERE st.id = sqlc.arg('id') AND st.id = previous.id
      AND st.refresh_token_hash = sqlc.arg('old_refresh_token_hash')
      AND st.refresh_expires_at > NOW()
    RETURNING st.id, previous.refresh_expires_at
)
INSERT INTO used_session_refresh_tokens (token_hash, session_id, expires_at)
SELECT sqlc.arg('old_refresh_token_hash'), id, refresh_expires_at FROM rotated;

-- name: TouchSessionLastUsed :exec
UPDATE session_tokens SET last_used_at = NOW() WHERE id = $1;

-- name: DeleteSessionToken :exec
DELETE FROM session_tokens WHERE id = $1;

-- name: DeleteSessionTokenByAccessHash :exec
DELETE FROM session_tokens WHERE access_token_hash = $1;

-- name: DeleteWebSessionByAccessHash :exec
DELETE FROM session_tokens
WHERE kind = 'web'
  AND id = (SELECT st.id FROM session_tokens st WHERE st.access_token_hash = $1);

-- name: DeleteExpiredUsedRefreshTokens :exec
DELETE FROM used_session_refresh_tokens WHERE expires_at <= NOW();

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

-- name: CreateAPIToken :one
INSERT INTO api_tokens (id, token_hash, name, entity_type, entity_id, scopes, created_by, expires_at, display_suffix)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, name, entity_type, entity_id, scopes, created_by, created_at, expires_at, last_used_at, display_suffix;

-- name: GetAPIToken :one
SELECT t.id, t.name, t.entity_type, t.entity_id, t.scopes, t.created_by,
       t.created_at, t.expires_at, t.last_used_at, t.display_suffix, t.personal_user_id,
       COALESCE((SELECT JSON_AGG(JSON_BUILD_OBJECT('scope', us.scope, 'entity_type', us.entity_type, 'entity_id', us.entity_id))
                 FROM user_scopes us WHERE us.user_id = t.personal_user_id), '[]'::json)::json AS live_scopes
FROM api_tokens t
LEFT JOIN users u ON u.id = t.personal_user_id
WHERE t.token_hash = $1 AND t.expires_at > NOW()
  AND (t.entity_type != 'user' OR u.status = 'active');

-- name: TouchAPITokenLastUsed :exec
UPDATE api_tokens SET last_used_at = NOW() WHERE id = $1;

-- name: DeleteAPIToken :exec
DELETE FROM api_tokens WHERE id = $1;

-- name: DeleteAPITokenByHash :exec
DELETE FROM api_tokens WHERE token_hash = $1;

-- name: DeleteExpiredAPITokens :exec
DELETE FROM api_tokens WHERE expires_at < NOW();

-- name: ListAPITokensForEntity :many
SELECT id, name, entity_type, entity_id, scopes, created_by, created_at, expires_at, last_used_at, display_suffix
FROM api_tokens
WHERE entity_type = $1 AND entity_id = $2
ORDER BY created_at DESC;

-- name: DeleteAPITokensForEntity :exec
DELETE FROM api_tokens WHERE entity_type = $1 AND entity_id = $2;

-- name: GetAPITokenByNameAndEntity :one
SELECT id, name, entity_type, entity_id, scopes, created_by, created_at, expires_at, last_used_at, display_suffix
FROM api_tokens
WHERE name = $1 AND entity_type = $2 AND entity_id = $3;

-- name: DeleteAPITokenByNameAndEntity :exec
DELETE FROM api_tokens WHERE name = $1 AND entity_type = $2 AND entity_id = $3;

