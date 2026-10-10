-- name: GetEnvironmentKey :one
SELECT * FROM environment_keys WHERE environment_id = $1;

-- name: InsertEnvironmentKey :one
INSERT INTO environment_keys (environment_id, provider, kek_id, wrapped_dek, format_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (environment_id) DO NOTHING
RETURNING *;

-- name: SetLockTimeout :exec
SELECT set_config('lock_timeout', sqlc.arg(timeout)::text, true);

-- name: LockEnvironmentKey :one
SELECT * FROM environment_keys WHERE environment_id = $1 FOR UPDATE;

-- name: LockEnvironmentKeyShared :one
SELECT * FROM environment_keys WHERE environment_id = $1 FOR SHARE;

-- name: RewrapEnvironmentKey :execrows
UPDATE environment_keys
SET provider = sqlc.arg(provider),
    kek_id = sqlc.arg(kek_id),
    wrapped_dek = sqlc.arg(wrapped_dek),
    rewrapped_at = NOW()
WHERE environment_id = sqlc.arg(environment_id)
  AND wrapped_dek = sqlc.arg(previous_wrapped_dek);

-- name: ListEnvironmentKeys :many
SELECT * FROM environment_keys ORDER BY environment_id;

-- name: ListSecretNames :many
SELECT name FROM secrets WHERE environment_id = $1 ORDER BY name;

-- name: ListSecrets :many
SELECT name, version, updated_by, created_at, updated_at
FROM secrets
WHERE environment_id = $1
ORDER BY name;

-- name: CountSecrets :one
SELECT COUNT(*) FROM secrets WHERE environment_id = $1;

-- name: ListSecretCiphertexts :many
SELECT name, version, nonce, ciphertext, format_version
FROM secrets
WHERE environment_id = $1 AND name = ANY(sqlc.arg(names)::text[])
ORDER BY name;

-- name: UpsertSecrets :exec
INSERT INTO secrets (environment_id, name, version, nonce, ciphertext, format_version, updated_by)
SELECT sqlc.arg(environment_id),
       unnest(sqlc.arg(names)::text[]),
       unnest(sqlc.arg(versions)::int[]),
       unnest(sqlc.arg(nonces)::bytea[]),
       unnest(sqlc.arg(ciphertexts)::bytea[]),
       unnest(sqlc.arg(format_versions)::smallint[]),
       sqlc.arg(updated_by)
ON CONFLICT (environment_id, name) DO UPDATE
SET version = EXCLUDED.version,
    nonce = EXCLUDED.nonce,
    ciphertext = EXCLUDED.ciphertext,
    format_version = EXCLUDED.format_version,
    updated_by = EXCLUDED.updated_by,
    updated_at = NOW();

-- name: DeleteSecrets :many
DELETE FROM secrets
WHERE environment_id = $1 AND name = ANY(sqlc.arg(names)::text[])
RETURNING name;

-- name: ListServiceSecretSizes :many
SELECT p.id AS placement_id,
       r.name AS service,
       SUM(octet_length(s.name))::bigint AS name_bytes,
       SUM(octet_length(s.ciphertext))::bigint AS ciphertext_bytes,
       COUNT(*)::bigint AS secret_count
FROM placements p
JOIN resources r ON r.id = p.resource_id
JOIN secrets s ON s.environment_id = p.environment_id AND s.name = ANY(p.secret_names)
WHERE p.environment_id = sqlc.arg(environment_id)
  AND NOT p.desired_deleted
  AND p.secret_names && sqlc.arg(names)::text[]
GROUP BY p.id, r.name
ORDER BY r.name, p.id;
