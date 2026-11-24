-- name: GetAuthState :one
SELECT value FROM auth_states WHERE key = $1 AND expires_at > NOW();

-- A new key is stored only while fewer than max_entries live rows exist.
-- name: SetAuthState :execrows
INSERT INTO auth_states (key, value, expires_at)
SELECT @key::text, @value::bytea, NOW() + make_interval(secs => @ttl_seconds::float8)
WHERE EXISTS (SELECT 1 FROM auth_states WHERE key = @key::text)
    OR (SELECT count(*) FROM (SELECT 1 FROM auth_states WHERE expires_at > NOW() LIMIT @max_entries::int) live)
        < @max_entries::int
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at;

-- An expired row counts as absent, so its key can be stored again. A full table stores nothing.
-- name: SetAuthStateIfAbsent :execrows
INSERT INTO auth_states (key, value, expires_at)
SELECT @key::text, @value::bytea, NOW() + make_interval(secs => @ttl_seconds::float8)
WHERE (SELECT count(*) FROM (SELECT 1 FROM auth_states WHERE expires_at > NOW() LIMIT @max_entries::int) live)
    < @max_entries::int
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at
WHERE auth_states.expires_at <= NOW();

-- name: TakeAuthState :one
DELETE FROM auth_states WHERE key = $1 AND expires_at > NOW() RETURNING value;

-- name: DeleteExpiredAuthStates :exec
DELETE FROM auth_states WHERE expires_at <= NOW();
