-- +goose Up
CREATE TYPE entity_type AS ENUM ('system', 'organization', 'workspace', 'resource', 'user');

CREATE TABLE user_scopes (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope TEXT NOT NULL, -- e.g. 'read', 'write', 'admin'
    entity_type entity_type NOT NULL,
    entity_id UUID NOT NULL, -- e.g. organization_id or workspace_id
    organization_ref UUID GENERATED ALWAYS AS (
        CASE WHEN entity_type = 'organization' THEN entity_id END
    ) STORED REFERENCES organizations(id) ON DELETE CASCADE,
    workspace_ref UUID GENERATED ALWAYS AS (
        CASE WHEN entity_type = 'workspace' THEN entity_id END
    ) STORED REFERENCES workspaces(id) ON DELETE CASCADE,
    resource_ref UUID GENERATED ALWAYS AS (
        CASE WHEN entity_type = 'resource' THEN entity_id END
    ) STORED REFERENCES resources(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, scope, entity_type, entity_id)
);

-- what scopes does user x have on entity y?
CREATE INDEX user_scopes_user_entity_idx ON user_scopes (user_id, entity_type, entity_id);

-- what users have scope z on entity y?
CREATE INDEX user_scopes_entity_scope_idx ON user_scopes (entity_type, entity_id, scope);
CREATE INDEX user_scopes_organization_idx ON user_scopes (organization_ref) WHERE organization_ref IS NOT NULL;
CREATE INDEX user_scopes_workspace_idx ON user_scopes (workspace_ref) WHERE workspace_ref IS NOT NULL;
CREATE INDEX user_scopes_resource_idx ON user_scopes (resource_ref) WHERE resource_ref IS NOT NULL;

CREATE TABLE system_bootstrap (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    bootstrapped_by UUID REFERENCES users(id) ON DELETE SET NULL,
    bootstrapped_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- session tokens: ephemeral, unlogged, scopes always resolved live from user_scopes
-- token format: loco_s_<base64url(32 random bytes)>  access token
--               loco_r_<base64url(32 random bytes)>  refresh token
-- stored as sha256(full_token_string), never the raw token
CREATE UNLOGGED TABLE session_tokens (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    access_token_hash TEXT NOT NULL UNIQUE,
    refresh_token_hash TEXT NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    identity_id UUID REFERENCES identities(id) ON DELETE CASCADE,
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX session_tokens_user_idx ON session_tokens (user_id);
CREATE INDEX session_tokens_identity_idx ON session_tokens (identity_id);
CREATE INDEX session_tokens_access_expires_idx ON session_tokens (access_expires_at);
-- cleanup cron uses refresh expiry to determine if a session is fully dead
CREATE INDEX session_tokens_refresh_expires_idx ON session_tokens (refresh_expires_at);

-- api tokens: logged, durable, scopes baked in at creation and never change
-- token format: loco_k_<base64url(32 random bytes)>
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
CREATE INDEX api_tokens_created_by_idx ON api_tokens (created_by);

CREATE TABLE invitations (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    invited_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    claimed_by UUID REFERENCES users(id),
    claimed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    delivery_error TEXT
);

CREATE UNIQUE INDEX invitations_open_idx ON invitations (org_id, lower(email)) WHERE claimed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invitations_email_open_idx ON invitations (lower(email)) WHERE claimed_at IS NULL AND revoked_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS invitations;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS session_tokens;
DROP TABLE IF EXISTS system_bootstrap;
DROP TABLE IF EXISTS user_scopes;
DROP TYPE IF EXISTS entity_type;
