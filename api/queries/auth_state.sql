-- name: GetAuthState :one
SELECT value FROM auth_states WHERE key = $1 AND expires_at > NOW();

-- name: SetAuthState :exec
INSERT INTO auth_states (key, value, client, expires_at)
VALUES (@key::text, @value::bytea, @client::text, NOW() + make_interval(secs => @ttl_seconds::float8))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, client = EXCLUDED.client, expires_at = EXCLUDED.expires_at;

-- Stores a sign-in start for client while fewer than max_entries sign-ins are in progress and client
-- has fewer than max_per_client. An expired row counts as absent, so its key can be stored again.
-- name: StartAuthState :execrows
INSERT INTO auth_states (key, value, client, expires_at)
SELECT @key::text, @value::bytea, @client::text, NOW() + make_interval(secs => @ttl_seconds::float8)
WHERE (SELECT count(*) FROM (
        SELECT 1 FROM auth_states WHERE client <> '' AND expires_at > NOW() LIMIT @max_entries::int
    ) live)
        < @max_entries::int
    AND (SELECT count(*) FROM (
        SELECT 1 FROM auth_states WHERE client = @client::text AND expires_at > NOW() LIMIT @max_per_client::int
    ) mine) < @max_per_client::int
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, client = EXCLUDED.client, expires_at = EXCLUDED.expires_at
WHERE auth_states.expires_at <= NOW();

-- name: TakeAuthState :one
DELETE FROM auth_states WHERE key = $1 AND expires_at > NOW() RETURNING value;

-- name: DeleteExpiredAuthStates :exec
DELETE FROM auth_states WHERE expires_at <= NOW();
