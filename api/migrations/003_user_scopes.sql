CREATE TYPE entity_type AS ENUM ('system', 'organization', 'workspace', 'app', 'user');
CREATE TYPE entity_scope AS (
    scope TEXT,
    entity_type entity_type,
    entity_id INTEGER
);

CREATE TABLE user_scopes (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope TEXT NOT NULL, -- e.g. 'read', 'write', 'admin'
    entity_type entity_type NOT NULL, -- e.g. 'organization', 'workspace', 'app', will never be 'user' since users cannot have scopes on themselves
    entity_id BIGINT NOT NULL, -- e.g. organization_id or workspace_id
    UNIQUE (user_id, scope, entity_type, entity_id)
);

-- what scopes does user x have on entity y?
CREATE INDEX user_scopes_user_entity_idx ON user_scopes (user_id, entity_type, entity_id);

-- what users have scope z on entity y?
CREATE INDEX user_scopes_entity_scope_idx ON user_scopes (entity_type, entity_id, scope);

-- what scopes does user x have?
CREATE INDEX user_scopes_user_idx ON user_scopes (user_id);

-- "cost-saving measure"
CREATE UNLOGGED TABLE tokens (
    token TEXT PRIMARY KEY,
    scopes JSONB NOT NULL, -- list of entity scopes associated with the token
    entity_type entity_type NOT NULL, --  e.g. 'organization', 'workspace', 'app', 'user'
    entity_id BIGINT NOT NULL, -- on behalf of which entity the token is issued (e.g. organization_id or workspace_id)
    expires_at TIMESTAMPTZ NOT NULL
);

-- which tokens exist on behalf of entity y?
CREATE INDEX tokens_entity_idx ON tokens (entity_type, entity_id);

CREATE VIEW user_with_scopes_view AS
SELECT 
    u.id,
    u.external_id,
    u.email,
    u.name,
    u.avatar_url,
    u.created_at,
    u.updated_at,
    COALESCE(
        JSON_AGG(
            JSON_BUILD_OBJECT(
                'scope', us.scope,
                'entity_type', us.entity_type,
                'entity_id', us.entity_id
            )
        ) FILTER (WHERE us.user_id IS NOT NULL),
        '[]'
    ) AS scopes
FROM users u
LEFT JOIN user_scopes us ON u.id = us.user_id
GROUP BY u.id, u.external_id, u.email, u.name, u.avatar_url, u.created_at, u.updated_at;