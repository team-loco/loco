-- +goose Up
CREATE TABLE org_domains (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    verification_token TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    auto_join_scope TEXT CHECK (auto_join_scope IN ('read', 'write', 'admin')),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, domain)
);

CREATE UNIQUE INDEX org_domains_verified_idx ON org_domains (domain) WHERE verified_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS org_domains;
