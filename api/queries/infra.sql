-- name: EnsureInfraStack :one
INSERT INTO infra_stacks (environment_id, name) VALUES ($1, $2)
ON CONFLICT (environment_id, name) DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: GetInfraStack :one
SELECT * FROM infra_stacks WHERE environment_id = $1 AND name = $2;

-- name: UpdateInfraStackManifest :exec
UPDATE infra_stacks SET manifest = $2 WHERE id = $1;

-- name: ListInfraStackResources :many
SELECT * FROM resources WHERE stack_id = $1 ORDER BY service_key;

-- name: LockInfraEnvironment :one
SELECT * FROM environments WHERE id = $1 FOR UPDATE;

-- name: StoreInfraPlan :one
INSERT INTO infra_plans (
    workspace_id, environment_id, stack_name, expected_revision,
    manifest_digest, source_digest, payload, plan, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id;

-- name: GetInfraPlan :one
SELECT * FROM infra_plans WHERE id = $1;

-- name: UpdateInfraPlanProjection :exec
UPDATE infra_plans SET plan = $2 WHERE id = $1;

-- name: GetInfraApply :one
SELECT * FROM infra_applies WHERE plan_id = $1;

-- name: StoreInfraApply :one
INSERT INTO infra_applies (plan_id, deployment_ids) VALUES ($1, $2) RETURNING id;

-- name: UpdateInfraResource :exec
UPDATE resources SET name = $2, description = $3, spec = $4, updated_at = NOW() WHERE id = $1;

-- name: ResetInfraPrimaryRegion :exec
UPDATE resource_regions SET is_primary = false WHERE resource_id = $1 AND is_primary;

-- name: UpsertInfraRegion :one
INSERT INTO resource_regions (resource_id, region, is_primary, status)
VALUES ($1, $2, $3, 'desired')
ON CONFLICT (resource_id, region) DO UPDATE SET is_primary = EXCLUDED.is_primary, status = 'desired'
RETURNING *;

-- name: RetireInfraRegion :exec
UPDATE resource_regions SET status = 'removing', is_primary = false WHERE resource_id = $1 AND region = $2;

-- name: DeleteInfraDomains :exec
DELETE FROM resource_domains WHERE resource_id = $1;

-- name: CreateInfraSecretVersion :one
INSERT INTO infra_secret_versions (environment_id, name, ciphertext) VALUES ($1, $2, $3) RETURNING id;

-- name: GetInfraSecretVersion :one
SELECT * FROM infra_secret_versions WHERE id = $1 AND environment_id = $2;

-- name: GetLatestInfraSecretVersion :one
SELECT * FROM infra_secret_versions WHERE environment_id = $1 AND name = $2
ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: UpdateInfraResourceVariables :exec
UPDATE resources SET variable_values = $2 WHERE id = $1;
