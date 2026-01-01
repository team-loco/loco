-- name: CreateResourceDomain :one
INSERT INTO resource_domains (
    resource_id,
    domain,
    domain_source,
    subdomain_label,
    platform_domain_id,
    is_primary
)
VALUES ($1, $2, $3, $4, $5, $6)
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

-- name: GetDomainByResourceId :one
SELECT 
    rd.*,
    pd.domain as platform_base_domain
FROM resource_domains rd
LEFT JOIN platform_domains pd ON rd.platform_domain_id = pd.id
WHERE rd.resource_id = $1;

-- name: GetResourceDomainByID :one
SELECT 
    rd.id,
    rd.resource_id,
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

-- name: GetResourceDomainCount :one
SELECT COUNT(*) as count FROM resource_domains WHERE resource_id = $1;

-- name: UpdateResourceDomainPrimary :exec
UPDATE resource_domains
SET is_primary = false
WHERE resource_id = $1;

-- name: SetResourceDomainPrimary :one
UPDATE resource_domains
SET is_primary = true
WHERE id = $1 AND resource_id = $2
RETURNING id;

-- name: UpdateResourceDomain :one
UPDATE resource_domains
SET domain = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING id;

-- name: DeleteResourceDomain :exec
DELETE FROM resource_domains WHERE id = $1;
