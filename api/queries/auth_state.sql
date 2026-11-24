-- name: GetAuthState :one
SELECT value FROM auth_states WHERE key = $1 AND expires_at > NOW();

-- Entries sharing a started_at belong to one started sign-in.
-- name: SetAuthState :exec
INSERT INTO auth_states (key, value, started_at, expires_at)
VALUES (
    @key::text, @value::bytea, sqlc.narg('started_at')::timestamptz,
    NOW() + make_interval(secs => @ttl_seconds::float8)
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, started_at = EXCLUDED.started_at, expires_at = EXCLUDED.expires_at;

-- Stores the entries of one new sign-in after deleting the sign-ins older than the newest keep, and
-- stores none of them while any of their keys is live. An expired row counts as absent, so its key can
-- be stored again.
-- name: StartAuthStates :execrows
WITH evicted AS (
    DELETE FROM auth_states
    WHERE started_at <= (
        SELECT DISTINCT started_at FROM auth_states
        WHERE started_at IS NOT NULL
        ORDER BY started_at DESC
        OFFSET @keep::int
        LIMIT 1
    )
)
INSERT INTO auth_states (key, value, started_at, expires_at)
SELECT unnest(@keys::text[]), unnest(@values::bytea[]), NOW(), NOW() + make_interval(secs => @ttl_seconds::float8)
WHERE NOT EXISTS (SELECT 1 FROM auth_states WHERE key = ANY(@keys::text[]) AND expires_at > NOW())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, started_at = EXCLUDED.started_at, expires_at = EXCLUDED.expires_at
WHERE auth_states.expires_at <= NOW();

-- name: TakeAuthState :one
DELETE FROM auth_states WHERE key = $1 AND expires_at > NOW()
RETURNING value, started_at, EXTRACT(EPOCH FROM expires_at - NOW())::float8 AS ttl_seconds;

-- name: DeleteExpiredAuthStates :exec
DELETE FROM auth_states WHERE expires_at <= NOW();
