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
SELECT user_id, scope, entity_type, entity_id FROM user_scopes WHERE user_id = $1 AND entity_type = $2 AND entity_id = $3;

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
    
    -- Apps in the workspaces
    SELECT 
        'app'::entity_type,
        a.id,
        a.name
    FROM apps a
    INNER JOIN entity_hierarchy eh ON eh.entity_type = 'workspace' AND eh.entity_id = a.workspace_id
)
SELECT DISTINCT
    us.user_id,
    us.scope,
    us.entity_type,
    us.entity_id
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
    
    -- Apps in the workspace
    SELECT 
        'app'::entity_type,
        a.id,
        a.name
    FROM apps a
    INNER JOIN entity_hierarchy eh ON eh.entity_type = 'workspace' AND eh.entity_id = a.workspace_id
)
SELECT DISTINCT
    us.user_id,
    us.scope,
    us.entity_type,
    us.entity_id
FROM user_scopes us
INNER JOIN entity_hierarchy eh ON us.entity_type = eh.entity_type AND us.entity_id = eh.entity_id
WHERE us.user_id = $2
ORDER BY us.entity_type, us.entity_id, us.scope;

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
