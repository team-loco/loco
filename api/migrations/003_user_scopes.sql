CREATE TYPE entity_type AS ENUM ('system', 'organization', 'workspace', 'app', 'user');

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

-- what scopes are associated with token x?
CREATE INDEX tokens_scopes_idx ON tokens (token);

-- which tokens exist on behalf of entity y?
CREATE INDEX tokens_entity_idx ON tokens (entity_type, entity_id);