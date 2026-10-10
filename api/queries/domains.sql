-- name: CreateResourceDomain :one
INSERT INTO resource_domains (
    resource_id,
    environment_id,
    domain,
    domain_source,
    subdomain_label,
    platform_domain_id,
    is_primary
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: CreatePlatformDomain :one
INSERT INTO platform_domains (domain, is_active)
VALUES ($1, $2)
RETURNING id;

-- name: GetPlatformDomain :one
SELECT * FROM platform_domains
WHERE id = $1;

-- name: GetPlatformDomainByName :one
SELECT * FROM platform_domains
WHERE domain = $1;

-- name: ListActivePlatformDomains :many
SELECT * FROM platform_domains
WHERE is_active = true
ORDER BY domain;

-- name: ListPlatformDomains :many
SELECT * FROM platform_domains
WHERE (sqlc.narg('active_only')::boolean IS NULL OR is_active = sqlc.narg('active_only')::boolean)
ORDER BY domain;

-- name: DeactivatePlatformDomain :one
UPDATE platform_domains
SET is_active = false
WHERE id = $1
RETURNING id;

-- name: CheckDomainAvailability :one
SELECT NOT EXISTS(
    SELECT 1 FROM resource_domains
    WHERE domain = $1
) as is_available;

-- name: GetPrimaryResourceDomain :one
SELECT domain
FROM resource_domains
WHERE resource_id = $1 AND environment_id = $2 AND is_primary;

-- name: GetResourceDomainByID :one
SELECT 
    rd.id,
    rd.resource_id,
    rd.environment_id,
    rd.domain,
    rd.domain_source,
    rd.subdomain_label,
    rd.platform_domain_id,
    rd.is_primary,
    rd.created_at,
    rd.updated_at
FROM resource_domains rd
WHERE rd.id = $1;

-- name: ListResourceDomains :many
SELECT 
    rd.id,
    rd.resource_id,
    rd.environment_id,
    rd.domain,
    rd.domain_source,
    rd.subdomain_label,
    rd.platform_domain_id,
    rd.is_primary,
    rd.created_at,
    rd.updated_at
FROM resource_domains rd
WHERE rd.resource_id = $1
ORDER BY rd.is_primary DESC, rd.created_at ASC;

-- name: ListResourceDomainsForResources :many
SELECT
    rd.id,
    rd.resource_id,
    rd.environment_id,
    rd.domain,
    rd.domain_source,
    rd.subdomain_label,
    rd.platform_domain_id,
    rd.is_primary,
    rd.created_at,
    rd.updated_at
FROM resource_domains rd
WHERE rd.resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
ORDER BY rd.resource_id, rd.is_primary DESC, rd.created_at ASC;

-- name: ListEnvironmentResourceDomains :many
SELECT
    rd.id,
    rd.resource_id,
    rd.environment_id,
    rd.domain,
    rd.domain_source,
    rd.subdomain_label,
    rd.platform_domain_id,
    rd.is_primary,
    rd.created_at,
    rd.updated_at
FROM resource_domains rd
WHERE rd.resource_id = ANY(sqlc.arg(resource_ids)::uuid[]) AND rd.environment_id = sqlc.arg(environment_id)
ORDER BY rd.resource_id, rd.is_primary DESC, rd.created_at ASC;

-- name: ListAllLocoOwnedDomains :many
SELECT 
    rd.id,
    rd.domain,
    r.name as resource_name,
    r.id as resource_id,
    pd.domain as platform_domain
FROM resource_domains rd
JOIN resources r ON rd.resource_id = r.id
JOIN platform_domains pd ON rd.platform_domain_id = pd.id
WHERE rd.domain_source = 'platform_provided'
ORDER BY rd.created_at DESC;

-- name: ResourceHasPrimaryDomain :one
SELECT EXISTS(
    SELECT 1 FROM resource_domains
    WHERE resource_id = $1 AND environment_id = $2 AND is_primary
) AS has_primary;

-- name: GetResourceDomainCount :one
SELECT COUNT(*) as count FROM resource_domains WHERE resource_id = $1 AND environment_id = $2;

-- name: UpdateResourceDomainPrimary :exec
UPDATE resource_domains
SET is_primary = false
WHERE resource_id = $1 AND environment_id = $2;

-- name: SetResourceDomainPrimary :one
UPDATE resource_domains
SET is_primary = true
WHERE id = $1 AND resource_id = $2 AND environment_id = $3
RETURNING id;

-- name: UpdateResourceDomain :one
UPDATE resource_domains
SET domain = $2,
    subdomain_label = sqlc.narg('subdomain_label'),
    updated_at = NOW()
WHERE id = $1
RETURNING id;

-- name: DeleteResourceDomain :exec
DELETE FROM resource_domains WHERE id = $1;
