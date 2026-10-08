-- +goose Up
CREATE TABLE org_sso (
    org_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL UNIQUE,
    issuer TEXT NOT NULL,
    require_sso BOOLEAN NOT NULL DEFAULT false,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS org_sso;
