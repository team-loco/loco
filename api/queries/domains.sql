-- ============================================================================
-- PLATFORM DOMAIN QUERIES
-- ============================================================================

-- name: CreateAppDomain :one
INSERT INTO app_domains (
    app_id,
    domain,
    domain_source,
    subdomain_label,
    platform_domain_id,
    is_primary
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreatePlatformDomain :one
INSERT INTO platform_domains (domain, is_active)
VALUES ($1, $2)
RETURNING *;

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
RETURNING *;

-- name: CheckDomainAvailability :one
SELECT NOT EXISTS(
    SELECT 1 FROM app_domains
    WHERE domain = $1
) as is_available;

-- name: GetDomainByAppId :one
SELECT 
    ad.*,
    pd.domain as platform_base_domain
FROM app_domains ad
LEFT JOIN platform_domains pd ON ad.platform_domain_id = pd.id
WHERE ad.app_id = $1;

-- name: GetAppDomainByID :one
SELECT 
    ad.id,
    ad.app_id,
    ad.domain,
    ad.domain_source,
    ad.subdomain_label,
    ad.platform_domain_id,
    ad.is_primary,
    ad.created_at,
    ad.updated_at
FROM app_domains ad
WHERE ad.id = $1;

-- name: ListAppDomains :many
SELECT 
    ad.id,
    ad.app_id,
    ad.domain,
    ad.domain_source,
    ad.subdomain_label,
    ad.platform_domain_id,
    ad.is_primary,
    ad.created_at,
    ad.updated_at
FROM app_domains ad
WHERE ad.app_id = $1
ORDER BY ad.is_primary DESC, ad.created_at ASC;

-- name: ListAllLocoOwnedDomains :many
SELECT 
    ad.id,
    ad.domain,
    a.name as app_name,
    a.id as app_id,
    pd.domain as platform_domain
FROM app_domains ad
JOIN apps a ON ad.app_id = a.id
JOIN platform_domains pd ON ad.platform_domain_id = pd.id
WHERE ad.domain_source = 'platform_provided'
ORDER BY ad.created_at DESC;

-- name: GetAppDomainCount :one
SELECT COUNT(*) as count FROM app_domains WHERE app_id = $1;

-- name: UpdateAppDomainPrimary :exec
UPDATE app_domains
SET is_primary = false
WHERE app_id = $1;

-- name: SetAppDomainPrimary :one
UPDATE app_domains
SET is_primary = true
WHERE id = $1 AND app_id = $2
RETURNING *;

-- name: UpdateAppDomain :one
UPDATE app_domains
SET domain = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteAppDomain :exec
DELETE FROM app_domains WHERE id = $1;
