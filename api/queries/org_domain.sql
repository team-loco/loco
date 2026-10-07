-- name: CreateOrgDomain :one
INSERT INTO org_domains (org_id, domain, verification_token, created_by)
VALUES ($1, $2, $3, $4)
RETURNING id, org_id, domain, verification_token, verified_at, auto_join_scope, created_by, created_at;

-- name: ListOrgDomains :many
SELECT id, org_id, domain, verification_token, verified_at, auto_join_scope, created_by, created_at
FROM org_domains
WHERE org_id = $1
ORDER BY domain;

-- name: GetOrgDomain :one
SELECT id, org_id, domain, verification_token, verified_at, auto_join_scope, created_by, created_at
FROM org_domains
WHERE id = $1 AND org_id = $2;

-- name: MarkOrgDomainVerified :one
UPDATE org_domains
SET verified_at = NOW()
WHERE id = $1 AND org_id = $2 AND verified_at IS NULL
RETURNING id, org_id, domain, verification_token, verified_at, auto_join_scope, created_by, created_at;

-- name: SetOrgDomainAutoJoin :one
UPDATE org_domains
SET auto_join_scope = sqlc.narg('auto_join_scope')
WHERE id = sqlc.arg('id') AND org_id = sqlc.arg('org_id')
RETURNING id, org_id, domain, verification_token, verified_at, auto_join_scope, created_by, created_at;

-- name: DeleteOrgDomain :execrows
DELETE FROM org_domains WHERE id = $1 AND org_id = $2;

-- name: GetAutoJoinForDomain :one
SELECT org_id, auto_join_scope
FROM org_domains
WHERE domain = $1 AND verified_at IS NOT NULL AND auto_join_scope IS NOT NULL;

-- name: ListUsersWithVerifiedEmailDomain :many
SELECT DISTINCT user_id
FROM identities
WHERE email_verified AND lower(split_part(email, '@', 2)) = $1;

-- name: LockOrg :one
SELECT id FROM organizations WHERE id = $1 FOR UPDATE;
