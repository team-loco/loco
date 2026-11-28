-- name: GetResourceEnvironment :one
SELECT * FROM resource_environments
WHERE resource_id = sqlc.arg(resource_id) AND environment_id = sqlc.arg(environment_id);

-- name: ListResourceEnvironments :many
SELECT * FROM resource_environments
WHERE resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
ORDER BY resource_id, environment_id;

-- name: UpsertResourceEnvironment :one
INSERT INTO resource_environments (resource_id, environment_id, workspace_id, spec, spec_version)
VALUES (sqlc.arg(resource_id), sqlc.arg(environment_id), sqlc.arg(workspace_id), sqlc.arg(spec), sqlc.arg(spec_version))
ON CONFLICT (resource_id, environment_id) DO UPDATE
SET spec = EXCLUDED.spec, spec_version = EXCLUDED.spec_version,
    revision = resource_environments.revision + 1, updated_at = NOW()
RETURNING *;

-- name: UpdateResourceEnvironmentSpec :execrows
UPDATE resource_environments
SET spec = sqlc.arg(spec), revision = revision + 1, updated_at = NOW()
WHERE resource_id = sqlc.arg(resource_id) AND environment_id = sqlc.arg(environment_id);

-- name: SetResourceEnvironmentDeployPolicy :execrows
UPDATE resource_environments
SET restart_policy = sqlc.narg(restart_policy),
    max_restarts = sqlc.narg(max_restarts),
    deploy_timeout_seconds = sqlc.narg(deploy_timeout_seconds),
    revision = revision + 1, updated_at = NOW()
WHERE resource_id = sqlc.arg(resource_id) AND environment_id = sqlc.arg(environment_id);

-- name: DeleteResourceEnvironment :execrows
DELETE FROM resource_environments
WHERE resource_id = sqlc.arg(resource_id) AND environment_id = sqlc.arg(environment_id);
