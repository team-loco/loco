-- name: AddSystemAdminEntries :exec
INSERT INTO system_admin_claims (email)
SELECT unnest(sqlc.arg('emails')::text[])
ON CONFLICT DO NOTHING;

-- name: RemoveSystemAdminEntriesExcept :many
WITH removed AS (
    DELETE FROM system_admin_claims
    WHERE NOT (email = ANY(sqlc.arg('emails')::text[]))
    RETURNING email, user_id
)
SELECT removed.email, removed.user_id, i.issuer, i.subject
FROM removed
LEFT JOIN identities i ON i.user_id = removed.user_id;

-- name: ClaimSystemAdmin :execrows
UPDATE system_admin_claims s
SET user_id = sqlc.arg('user_id')::uuid, claimed_at = NOW()
WHERE s.email = sqlc.arg('email')
    AND s.claimed_at IS NULL
    AND NOT EXISTS (SELECT 1 FROM system_admin_claims c WHERE c.user_id = sqlc.arg('user_id'));

-- name: RemoveSystemAdminScopeUnlessGranted :exec
DELETE FROM user_scopes s
WHERE s.user_id = sqlc.arg('user_id')
    AND s.scope = 'admin' AND s.entity_type = 'system' AND s.entity_id = '00000000-0000-0000-0000-000000000000'
    AND NOT EXISTS (SELECT 1 FROM system_admin_grants g WHERE g.user_id = sqlc.arg('user_id'));

-- name: ListSystemAdmins :many
SELECT u.id, u.email, i.issuer, i.subject,
    (c.user_id IS NOT NULL)::boolean AS configured,
    (g.user_id IS NOT NULL)::boolean AS granted
FROM user_scopes s
JOIN users u ON u.id = s.user_id
LEFT JOIN identities i ON i.user_id = u.id
LEFT JOIN system_admin_claims c ON c.user_id = u.id
LEFT JOIN system_admin_grants g ON g.user_id = u.id
WHERE s.scope = 'admin' AND s.entity_type = 'system' AND s.entity_id = '00000000-0000-0000-0000-000000000000'
ORDER BY u.email;

-- name: ListUnclaimedSystemAdminEmails :many
SELECT email FROM system_admin_claims WHERE user_id IS NULL ORDER BY email;

-- name: LockSystemAdmins :many
-- Locks every system:admin row so concurrent revocations cannot remove the last admin.
SELECT user_id FROM user_scopes
WHERE scope = 'admin' AND entity_type = 'system' AND entity_id = '00000000-0000-0000-0000-000000000000'
FOR UPDATE;

-- name: AddSystemAdminGrant :execrows
INSERT INTO system_admin_grants (user_id) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: RemoveSystemAdminGrant :execrows
DELETE FROM system_admin_grants WHERE user_id = $1;

-- name: SystemAdminClaimed :one
SELECT EXISTS (SELECT 1 FROM system_admin_claims WHERE user_id = $1);
