-- what scopes does user x have?
-- name: GetUserScopes :many
SELECT user_id, scope, entity_type, entity_id FROM user_scopes WHERE user_id = $1;

-- name: GetUserScopesByEmail :many
SELECT us.user_id, us.scope, us.entity_type, us.entity_id
FROM user_scopes us
JOIN users u ON us.user_id = u.id
WHERE u.email = $1;

-- what scopes does user x have on entity y?
-- name: GetUserScopesOnEntity :many
SELECT scope FROM user_scopes WHERE user_id = $1 AND entity_type = $2 AND entity_id = $3;


-- what users have scope z on entity y?
-- name: GetUsersWithScopeOnEntity :many
SELECT user_id FROM user_scopes WHERE entity_type = $1 AND entity_id = $2 AND scope = $3;

-- name: GetToken :one
SELECT scopes, entity_type, entity_id, expires_at FROM tokens WHERE token = $1;

-- which tokens exist on behalf of entity y?
-- name: GetTokensForEntity :many
SELECT token FROM tokens WHERE entity_type = $1 AND entity_id = $2;

-- name: AddUserScope :exec
INSERT INTO user_scopes (user_id, scope, entity_type, entity_id) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING;

-- name: RemoveUserScope :exec
DELETE FROM user_scopes WHERE user_id = $1 AND scope = $2 AND entity_type = $3 AND entity_id = $4;

-- name: RemoveAllScopesForUserOnEntity :exec
DELETE FROM user_scopes WHERE user_id = $1 AND entity_type = $2 AND entity_id = $3;

-- name: RemoveAllScopesForEntity :exec
DELETE FROM user_scopes WHERE entity_type = $1 AND entity_id = $2;

-- RemoveAllScopesForUser :exec
DELETE FROM user_scopes WHERE user_id = $1;

-- name: StoreToken :exec
INSERT INTO tokens (token, entity_type, entity_id, scopes, expires_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING;

-- name: DeleteToken :exec
DELETE FROM tokens WHERE token = $1;

-- name: DeleteTokensForEntity :exec
DELETE FROM tokens WHERE entity_type = $1 AND entity_id = $2;