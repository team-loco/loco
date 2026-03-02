CREATE TYPE entity_type AS ENUM ('system', 'organization', 'workspace', 'resource', 'user');
CREATE TYPE entity_scope AS (
    scope TEXT,
    entity_type entity_type,
    entity_id UUID
);

CREATE TABLE user_scopes (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope TEXT NOT NULL, -- e.g. 'read', 'write', 'admin'
    entity_type entity_type NOT NULL, -- e.g. 'organization', 'workspace', 'resource', will never be 'user' since users cannot have scopes on themselves
    entity_id UUID NOT NULL, -- e.g. organization_id or workspace_id
    UNIQUE (user_id, scope, entity_type, entity_id)
);

-- what scopes does user x have on entity y?
CREATE INDEX user_scopes_user_entity_idx ON user_scopes (user_id, entity_type, entity_id);

-- what users have scope z on entity y?
CREATE INDEX user_scopes_entity_scope_idx ON user_scopes (entity_type, entity_id, scope);

-- what scopes does user x have?
CREATE INDEX user_scopes_user_idx ON user_scopes (user_id);

-- session tokens: ephemeral, unlogged, scopes always resolved live from user_scopes
-- token format: loco_s_<base64url(uuidv7 bytes)>  access token
--               loco_r_<base64url(uuidv7 bytes)>  refresh token
-- stored as sha256(full_token_string), never the raw token
CREATE UNLOGGED TABLE session_tokens (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    access_token_hash TEXT NOT NULL UNIQUE,
    refresh_token_hash TEXT NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX session_tokens_user_idx ON session_tokens (user_id);
CREATE INDEX session_tokens_access_expires_idx ON session_tokens (access_expires_at);
-- cleanup cron uses refresh expiry to determine if a session is fully dead
CREATE INDEX session_tokens_refresh_expires_idx ON session_tokens (refresh_expires_at);

-- api tokens: logged, durable, scopes baked in at creation and never change
-- token format: loco_k_<base64url(uuidv7 bytes)>
-- stored as sha256(full_token_string), never the raw token
CREATE TABLE api_tokens (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    token_hash TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    entity_type entity_type NOT NULL,
    entity_id UUID NOT NULL,
    scopes JSONB NOT NULL,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    UNIQUE (name, entity_type, entity_id)
);

CREATE INDEX api_tokens_entity_idx ON api_tokens (entity_type, entity_id);
CREATE INDEX api_tokens_expires_idx ON api_tokens (expires_at);

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
