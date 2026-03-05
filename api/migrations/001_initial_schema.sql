-- Users table
CREATE TABLE
    IF NOT EXISTS users (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        external_id TEXT UNIQUE NOT NULL, -- provider:id format (e.g., github:nikumar1206)
        email TEXT UNIQUE NOT NULL,
        name TEXT,
        avatar_url TEXT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX IF NOT EXISTS idx_users_external_id ON users (external_id);

CREATE INDEX IF NOT EXISTS idx_users_email ON users (email);

CREATE INDEX IF NOT EXISTS idx_users_created_at_id_desc ON users (created_at DESC, id DESC);

-- Organizations table
CREATE TABLE
    IF NOT EXISTS organizations (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        name TEXT UNIQUE NOT NULL, -- Globally unique org name
        created_by UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX IF NOT EXISTS idx_organizations_name ON organizations (name);

CREATE INDEX IF NOT EXISTS idx_organizations_created_by ON organizations (created_by);

CREATE INDEX IF NOT EXISTS idx_organizations_created_at_id_desc ON organizations (created_at DESC, id DESC);

-- Workspaces table
CREATE TABLE
    IF NOT EXISTS workspaces (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        org_id UUID NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        description TEXT,
        created_by UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (org_id, name)
    );

CREATE INDEX IF NOT EXISTS idx_workspaces_org_id ON workspaces (org_id);

CREATE INDEX IF NOT EXISTS idx_workspaces_created_by ON workspaces (created_by);

CREATE INDEX IF NOT EXISTS idx_workspaces_org_id_created_at ON workspaces (org_id, created_at);

CREATE INDEX IF NOT EXISTS idx_workspaces_created_at_id_desc ON workspaces (created_at DESC, id DESC);