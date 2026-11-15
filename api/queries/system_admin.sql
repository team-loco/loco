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
