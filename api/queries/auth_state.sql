-- name: GetAuthState :one
SELECT value FROM auth_states WHERE key = $1 AND expires_at > NOW();

-- A started entry counts against the bound on started sign-ins.
-- name: SetAuthState :exec
INSERT INTO auth_states (key, value, started_at, expires_at)
VALUES (
    @key::text, @value::bytea, CASE WHEN @started::bool THEN NOW() END,
    NOW() + make_interval(secs => @ttl_seconds::float8)
)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, started_at = EXCLUDED.started_at, expires_at = EXCLUDED.expires_at;

-- Stores a started sign-in after deleting the started sign-ins older than the newest keep. An expired
-- row counts as absent, so its key can be stored again.
-- name: StartAuthState :execrows
WITH evicted AS (
    DELETE FROM auth_states
    WHERE key IN (
        SELECT key FROM auth_states
        WHERE started_at IS NOT NULL
        ORDER BY started_at DESC
        OFFSET @keep::int
        FOR UPDATE SKIP LOCKED
    )
)
INSERT INTO auth_states (key, value, started_at, expires_at)
VALUES (@key::text, @value::bytea, NOW(), NOW() + make_interval(secs => @ttl_seconds::float8))
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, started_at = EXCLUDED.started_at, expires_at = EXCLUDED.expires_at
WHERE auth_states.expires_at <= NOW();

-- name: TakeAuthState :one
DELETE FROM auth_states WHERE key = $1 AND expires_at > NOW() RETURNING value;

-- name: DeleteExpiredAuthStates :exec
DELETE FROM auth_states WHERE expires_at <= NOW();
