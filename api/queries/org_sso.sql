-- name: GetOrgSSO :one
SELECT org_id, connection_id, issuer, require_sso, created_by, created_at, updated_at
FROM org_sso
WHERE org_id = $1;

-- name: CreateOrgSSO :one
INSERT INTO org_sso (org_id, connection_id, issuer, created_by)
VALUES ($1, $2, $3, $4)
RETURNING org_id, connection_id, issuer, require_sso, created_by, created_at, updated_at;

-- name: SetOrgRequireSSO :one
UPDATE org_sso
SET require_sso = $2, updated_at = NOW()
WHERE org_id = $1
RETURNING org_id, connection_id, issuer, require_sso, created_by, created_at, updated_at;

-- name: DeleteOrgSSO :execrows
DELETE FROM org_sso WHERE org_id = $1;

-- name: ListVerifiedOrgDomainNames :many
SELECT domain
FROM org_domains
WHERE org_id = $1 AND verified_at IS NOT NULL
ORDER BY domain;

-- name: ListSSOGatedEntities :many
WITH requested AS (
    SELECT
        unnest(sqlc.arg('entity_types')::text[]) AS entity_type,
        unnest(sqlc.arg('entity_ids')::uuid[]) AS entity_id
),
owned AS (
    SELECT r.entity_type, r.entity_id, r.entity_id AS org_id
    FROM requested r
    WHERE r.entity_type = 'organization'
    UNION ALL
    SELECT r.entity_type, r.entity_id, w.org_id
    FROM requested r
    JOIN workspaces w ON w.id = r.entity_id
    WHERE r.entity_type = 'workspace'
    UNION ALL
    SELECT r.entity_type, r.entity_id, w.org_id
    FROM requested r
    JOIN resources res ON res.id = r.entity_id
    JOIN workspaces w ON w.id = res.workspace_id
    WHERE r.entity_type = 'resource'
)
SELECT o.entity_type::entity_type AS entity_type, o.entity_id::uuid AS entity_id, s.org_id, s.connection_id
FROM owned o
JOIN org_sso s ON s.org_id = o.org_id
WHERE s.require_sso;

-- name: SSOConnectionCoversDomain :one
SELECT EXISTS (
    SELECT 1
    FROM org_sso s
    JOIN org_domains d ON d.org_id = s.org_id AND d.verified_at IS NOT NULL
    WHERE s.connection_id = sqlc.arg('connection_id') AND s.issuer = sqlc.arg('issuer') AND d.domain = sqlc.arg('domain')
);
