-- name: AddSystemRoleEntries :exec
INSERT INTO system_role_claims (email, role)
SELECT unnest(sqlc.arg('emails')::text[]), sqlc.arg('role')::system_role
ON CONFLICT DO NOTHING;

-- name: ListSystemRoleEntriesExcept :many
SELECT email, user_id FROM system_role_claims
WHERE NOT (
    (role = 'admin' AND email = ANY(sqlc.arg('admins')::text[]))
    OR (role = 'read' AND email = ANY(sqlc.arg('readers')::text[]))
)
ORDER BY email;

-- name: LockSystemRoleEntriesExcept :many
SELECT email, user_id FROM system_role_claims
WHERE NOT (
    (role = 'admin' AND email = ANY(sqlc.arg('admins')::text[]))
    OR (role = 'read' AND email = ANY(sqlc.arg('readers')::text[]))
)
ORDER BY email
FOR UPDATE;

-- name: RemoveSystemRoleEntriesExcept :many
WITH removed AS (
    DELETE FROM system_role_claims
    WHERE NOT (
        (role = 'admin' AND email = ANY(sqlc.arg('admins')::text[]))
        OR (role = 'read' AND email = ANY(sqlc.arg('readers')::text[]))
    )
    RETURNING email, role, user_id
)
SELECT removed.email, removed.role, removed.user_id, i.issuer, i.subject
FROM removed
LEFT JOIN identities i ON i.user_id = removed.user_id;

-- name: ClaimSystemRole :execrows
UPDATE system_role_claims s
SET user_id = sqlc.arg('user_id')::uuid, claimed_at = NOW()
WHERE s.email = sqlc.arg('email')
    AND s.claimed_at IS NULL
    AND NOT EXISTS (SELECT 1 FROM system_role_claims c WHERE c.user_id = sqlc.arg('user_id'));

-- name: GetSystemRoleSources :one
SELECT c.role AS configured_role, g.role AS granted_role
FROM users u
LEFT JOIN system_role_claims c ON c.user_id = u.id
LEFT JOIN system_role_grants g ON g.user_id = u.id
WHERE u.id = $1;

-- name: RemoveSystemScopes :exec
DELETE FROM user_scopes WHERE user_id = $1 AND entity_type = 'system';

-- name: ListSystemRoleHolders :many
SELECT u.id, u.email, i.issuer, i.subject, c.role AS configured_role, g.role AS granted_role
FROM users u
LEFT JOIN identities i ON i.user_id = u.id
LEFT JOIN system_role_claims c ON c.user_id = u.id
LEFT JOIN system_role_grants g ON g.user_id = u.id
WHERE c.user_id IS NOT NULL OR g.user_id IS NOT NULL
ORDER BY u.email;

-- name: ListUnclaimedSystemRoleEmails :many
SELECT email, role FROM system_role_claims WHERE claimed_at IS NULL ORDER BY email;

-- name: LockSystemAdmins :many
-- Locks every system:admin row so concurrent changes cannot remove the last admin.
SELECT user_id FROM user_scopes
WHERE scope = 'admin' AND entity_type = 'system' AND entity_id = '00000000-0000-0000-0000-000000000000'
ORDER BY user_id
FOR UPDATE;

-- name: SetSystemRoleGrant :exec
INSERT INTO system_role_grants (user_id, role) VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: RemoveSystemRoleGrant :exec
DELETE FROM system_role_grants WHERE user_id = $1;
