-- name: GetAuthState :one
SELECT value FROM auth_states WHERE key = $1 AND expires_at > NOW();

-- name: SetAuthState :exec
INSERT INTO auth_states (key, value, expires_at)
VALUES ($1, $2, NOW() + make_interval(secs => @ttl_seconds::float8))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at;

-- An expired row counts as absent, so its key can be stored again.
-- name: SetAuthStateIfAbsent :execrows
INSERT INTO auth_states (key, value, expires_at)
VALUES ($1, $2, NOW() + make_interval(secs => @ttl_seconds::float8))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at
WHERE auth_states.expires_at <= NOW();

-- name: TakeAuthState :execrows
DELETE FROM auth_states WHERE key = $1 AND expires_at > NOW();

-- name: DeleteExpiredAuthStates :exec
DELETE FROM auth_states WHERE expires_at <= NOW();
