-- +goose Up
CREATE TYPE user_status AS ENUM ('active', 'suspended');

-- Users table
CREATE TABLE
    IF NOT EXISTS users (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        email TEXT UNIQUE NOT NULL,
        name TEXT,
        avatar_url TEXT,
        status user_status NOT NULL DEFAULT 'active',
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX IF NOT EXISTS idx_users_created_at_id_desc ON users (created_at DESC, id DESC);

CREATE TABLE
    IF NOT EXISTS identities (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
        issuer TEXT NOT NULL,
        subject TEXT NOT NULL,
        email TEXT,
        email_verified BOOLEAN NOT NULL DEFAULT false,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        last_login_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (issuer, subject)
    );

CREATE INDEX IF NOT EXISTS idx_identities_user_id ON identities (user_id);

-- Organizations table
CREATE TABLE
    IF NOT EXISTS organizations (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        name TEXT NOT NULL,
        created_by UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
        suspended_at TIMESTAMPTZ,
        suspension_reason TEXT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        CHECK ((suspended_at IS NULL) = (suspension_reason IS NULL))
    );

CREATE INDEX IF NOT EXISTS idx_organizations_created_by ON organizations (created_by);

CREATE INDEX IF NOT EXISTS idx_organizations_created_at_id_desc ON organizations (created_at DESC, id DESC);

-- Per-organization count limits a system admin set; a NULL column uses the install default
CREATE TABLE
    IF NOT EXISTS org_limit_overrides (
        org_id UUID PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
        workspaces_per_org INT CHECK (workspaces_per_org > 0),
        members_per_org INT CHECK (members_per_org > 0),
        invitations_per_org INT CHECK (invitations_per_org > 0),
        environments_per_workspace INT CHECK (environments_per_workspace > 0),
        resources_per_workspace INT CHECK (resources_per_workspace > 0),
        webhooks_per_workspace INT CHECK (webhooks_per_workspace > 0),
        tokens_per_entity INT CHECK (tokens_per_entity > 0),
        domains_per_resource INT CHECK (domains_per_resource > 0),
        secrets_per_resource INT CHECK (secrets_per_resource > 0),
        deployment_history INT CHECK (deployment_history > 0),
        build_history INT CHECK (build_history > 0),
        reason TEXT NOT NULL,
        updated_by UUID REFERENCES users (id) ON DELETE SET NULL,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

-- Workspaces table
CREATE TABLE
    IF NOT EXISTS workspaces (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        org_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        description TEXT,
        created_by UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX IF NOT EXISTS idx_workspaces_created_by ON workspaces (created_by);

CREATE INDEX IF NOT EXISTS idx_workspaces_org_id_created_at ON workspaces (org_id, created_at);

CREATE INDEX IF NOT EXISTS idx_workspaces_created_at_id_desc ON workspaces (created_at DESC, id DESC);

-- +goose Down
DROP TABLE IF EXISTS workspaces;
DROP TABLE IF EXISTS org_limit_overrides;
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS identities;
DROP TABLE IF EXISTS users;
DROP TYPE IF EXISTS user_status;
