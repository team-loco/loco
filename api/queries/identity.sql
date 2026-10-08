-- name: GetUserByIdentity :one
SELECT u.id, u.email, u.name, u.avatar_url, u.created_at, u.updated_at
FROM identities i
JOIN users u ON u.id = i.user_id
WHERE i.issuer = $1 AND i.subject = $2;

-- name: CreateIdentity :one
INSERT INTO identities (user_id, issuer, subject, email, email_verified)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, issuer, subject, email, email_verified, created_at, last_login_at;

-- name: TouchIdentity :exec
UPDATE identities
SET email = $3, email_verified = $4, last_login_at = NOW()
WHERE issuer = $1 AND subject = $2;


-- name: GetIdentity :one
SELECT id, user_id, issuer, subject, email, email_verified, created_at, last_login_at
FROM identities
WHERE issuer = $1 AND subject = $2;

-- name: ListIdentitiesForUser :many
SELECT id, user_id, issuer, subject, email, email_verified, created_at, last_login_at
FROM identities
WHERE user_id = $1
ORDER BY created_at;

-- name: UserHasUnverifiedIdentity :one
SELECT EXISTS (
    SELECT 1 FROM identities WHERE user_id = $1 AND NOT email_verified
);
