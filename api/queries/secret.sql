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

-- name: ListResourceSecretNames :many
SELECT r.name AS resource_name,
       s.name,
       octet_length(s.ciphertext)::bigint AS ciphertext_bytes
FROM secrets s
JOIN resources r ON r.id = s.resource_id
WHERE s.environment_id = $1
ORDER BY r.name, s.name;

-- name: ListSecrets :many
SELECT name, version, updated_by_type, updated_by_id, created_at, updated_at
FROM secrets
WHERE resource_id = $1 AND environment_id = $2
ORDER BY name;

-- name: CountSecrets :one
SELECT COUNT(*) FROM secrets WHERE resource_id = $1 AND environment_id = $2;

-- name: ListSecretCiphertexts :many
SELECT name, version, nonce, ciphertext, format_version
FROM secrets
WHERE resource_id = $1 AND environment_id = $2 AND name = ANY(sqlc.arg(names)::text[])
ORDER BY name;

-- name: UpsertSecrets :exec
INSERT INTO secrets (resource_id, environment_id, name, version, nonce, ciphertext, format_version, updated_by_type, updated_by_id)
SELECT sqlc.arg(resource_id),
       sqlc.arg(environment_id),
       unnest(sqlc.arg(names)::text[]),
       unnest(sqlc.arg(versions)::int[]),
       unnest(sqlc.arg(nonces)::bytea[]),
       unnest(sqlc.arg(ciphertexts)::bytea[]),
       unnest(sqlc.arg(format_versions)::smallint[]),
       sqlc.arg(updated_by_type),
       sqlc.arg(updated_by_id)
ON CONFLICT (resource_id, environment_id, name) DO UPDATE
SET version = EXCLUDED.version,
    nonce = EXCLUDED.nonce,
    ciphertext = EXCLUDED.ciphertext,
    format_version = EXCLUDED.format_version,
    updated_by_type = EXCLUDED.updated_by_type,
    updated_by_id = EXCLUDED.updated_by_id,
    updated_at = NOW();

-- name: DeleteSecrets :many
DELETE FROM secrets
WHERE resource_id = $1 AND environment_id = $2 AND name = ANY(sqlc.arg(names)::text[])
RETURNING name;

-- name: ListPlacementSecretSizes :many
SELECT p.id AS placement_id,
       SUM(octet_length(s.name))::bigint AS name_bytes,
       SUM(octet_length(s.ciphertext))::bigint AS ciphertext_bytes,
       COUNT(*)::bigint AS secret_count
FROM placements p
JOIN secrets s ON s.resource_id = p.resource_id
             AND s.environment_id = p.environment_id
             AND s.name = ANY(p.secret_names)
WHERE p.resource_id = sqlc.arg(resource_id)
  AND p.environment_id = sqlc.arg(environment_id)
  AND NOT p.desired_deleted
  AND p.secret_names && sqlc.arg(names)::text[]
GROUP BY p.id
ORDER BY p.id;

-- name: ListDeclaredSecretNames :many
SELECT DISTINCT declared.name::text AS name
FROM placements p, unnest(p.secret_names) AS declared(name)
WHERE p.resource_id = sqlc.arg(resource_id)
  AND p.environment_id = sqlc.arg(environment_id)
  AND NOT p.desired_deleted
  AND declared.name = ANY(sqlc.arg(names)::text[])
ORDER BY 1;
