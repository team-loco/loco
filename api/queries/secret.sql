-- name: GetEnvironmentKey :one
SELECT * FROM environment_keys WHERE environment_id = $1;

-- name: InsertEnvironmentKey :one
INSERT INTO environment_keys (environment_id, provider, kek_id, wrapped_dek, format_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (environment_id) DO NOTHING
RETURNING *;

-- name: SetLockTimeout :exec
SELECT set_config('lock_timeout', sqlc.arg(timeout)::text, true);

-- name: ResetLockTimeout :exec
SET LOCAL lock_timeout TO DEFAULT;

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

-- name: ListSecretSizes :many
SELECT name, octet_length(ciphertext)::bigint AS ciphertext_bytes
FROM secrets
WHERE environment_id = $1
ORDER BY name;

-- name: ListSecrets :many
SELECT name, version, updated_by_type, updated_by_id, created_at, updated_at
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
INSERT INTO secrets (environment_id, name, version, nonce, ciphertext, format_version,
                     updated_by_type, updated_by_id, created_by_type, created_by_id)
SELECT sqlc.arg(environment_id),
       unnest(sqlc.arg(names)::text[]),
       unnest(sqlc.arg(versions)::int[]),
       unnest(sqlc.arg(nonces)::bytea[]),
       unnest(sqlc.arg(ciphertexts)::bytea[]),
       unnest(sqlc.arg(format_versions)::smallint[]),
       sqlc.arg(updated_by_type),
       sqlc.arg(updated_by_id),
       sqlc.arg(updated_by_type),
       sqlc.arg(updated_by_id)
ON CONFLICT (environment_id, name) DO UPDATE
SET version = EXCLUDED.version,
    nonce = EXCLUDED.nonce,
    ciphertext = EXCLUDED.ciphertext,
    format_version = EXCLUDED.format_version,
    updated_by_type = EXCLUDED.updated_by_type,
    updated_by_id = EXCLUDED.updated_by_id,
    updated_at = NOW();

-- name: DeleteSecrets :many
DELETE FROM secrets
WHERE environment_id = $1 AND name = ANY(sqlc.arg(names)::text[])
RETURNING name;

-- name: ListPlacementSecretEnvs :many
SELECT id, secret_env
FROM placements
WHERE environment_id = $1 AND NOT desired_deleted AND secret_env <> '{}'::jsonb
ORDER BY id;

-- name: ListEnvironmentSecretCiphertexts :many
SELECT name, version, nonce, ciphertext, format_version
FROM secrets
WHERE environment_id = $1
ORDER BY name;

-- name: ReencryptSecrets :execrows
UPDATE secrets s
SET nonce = v.nonce,
    ciphertext = v.ciphertext
FROM (
    SELECT unnest(sqlc.arg(names)::text[]) AS name,
           unnest(sqlc.arg(nonces)::bytea[]) AS nonce,
           unnest(sqlc.arg(ciphertexts)::bytea[]) AS ciphertext
) AS v
WHERE s.environment_id = sqlc.arg(environment_id)
  AND s.name = v.name;

-- name: ListEnvironmentTargets :many
-- Every resource of the workspace with what references to it read in the environment: its latest
-- deployment there that was not canceled, and its primary domain there.
SELECT r.id, r.slug,
       d.spec AS deployment_spec,
       d.secret_env AS secret_env,
       dom.domain AS primary_domain
FROM resources r
LEFT JOIN LATERAL (
    SELECT dd.spec, dd.secret_env FROM deployments dd
    WHERE dd.resource_id = r.id AND dd.environment_id = sqlc.arg(environment_id) AND dd.status <> 'canceled'
    ORDER BY dd.created_at DESC, dd.id DESC
    LIMIT 1
) d ON true
LEFT JOIN resource_domains dom
    ON dom.resource_id = r.id AND dom.environment_id = sqlc.arg(environment_id) AND dom.is_primary
WHERE r.workspace_id = sqlc.arg(workspace_id)
ORDER BY r.slug;
