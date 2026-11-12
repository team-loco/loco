-- name: CreateInvitation :one
INSERT INTO invitations (org_id, email, invited_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RevokeExpiredInvitations :many
UPDATE invitations
SET revoked_at = NOW()
WHERE org_id = $1
  AND lower(email) = lower(sqlc.arg('email'))
  AND claimed_at IS NULL
  AND revoked_at IS NULL
  AND expires_at <= NOW()
RETURNING *;

-- name: ListOrgInvitations :many
SELECT *
FROM invitations
WHERE org_id = $1
  AND claimed_at IS NULL
  AND revoked_at IS NULL
  AND (sqlc.narg('page_token')::text IS NULL
       OR (created_at, id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: GetInvitation :one
SELECT * FROM invitations WHERE id = $1;

-- name: ListOpenInvitationsForEmail :many
SELECT *
FROM invitations
WHERE lower(email) = lower(sqlc.arg('email'))
  AND claimed_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > NOW()
ORDER BY id;

-- name: ClaimInvitation :one
UPDATE invitations
SET claimed_by = sqlc.arg('user_id')::uuid, claimed_at = NOW()
WHERE id = sqlc.arg('id')
  AND claimed_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > NOW()
RETURNING *;

-- name: RevokeInvitation :one
UPDATE invitations
SET revoked_at = NOW()
WHERE id = $1 AND claimed_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- name: ExtendInvitation :one
UPDATE invitations
SET expires_at = $2
WHERE id = $1 AND claimed_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- name: GetInvitationClaimDetails :one
SELECT o.name AS org_name, COALESCE(NULLIF(u.name, ''), u.email)::text AS inviter_name
FROM invitations i
JOIN organizations o ON o.id = i.org_id
JOIN users u ON u.id = i.invited_by
WHERE i.id = $1;
