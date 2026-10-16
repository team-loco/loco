-- name: UpsertPlacement :one
INSERT INTO placements (resource_id, cluster_id, region, deployment_id, desired_spec, environment_id, secret_names)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (resource_id, cluster_id) DO UPDATE
SET region = EXCLUDED.region,
    deployment_id = EXCLUDED.deployment_id,
    desired_spec = EXCLUDED.desired_spec,
    environment_id = EXCLUDED.environment_id,
    secret_names = EXCLUDED.secret_names,
    desired_deleted = false,
    desired_revision = placements.desired_revision + 1,
    applied_error = NULL,
    retry_after = NULL,
    apply_attempts = 0,
    apply_failed = false,
    updated_at = NOW()
RETURNING id, desired_revision;

-- name: MarkPlacementDeleted :many
UPDATE placements
SET desired_deleted = true,
    desired_spec = NULL,
    desired_revision = desired_revision + 1,
    applied_error = NULL,
    retry_after = NULL,
    apply_attempts = 0,
    apply_failed = false,
    updated_at = NOW()
WHERE resource_id = $1 AND cluster_id = $2 AND NOT desired_deleted
RETURNING id, cluster_id, desired_revision;

-- name: MarkResourcePlacementsDeleted :many
UPDATE placements
SET desired_deleted = true,
    desired_spec = NULL,
    desired_revision = desired_revision + 1,
    applied_error = NULL,
    retry_after = NULL,
    apply_attempts = 0,
    apply_failed = false,
    updated_at = NOW()
WHERE resource_id = $1 AND NOT desired_deleted
RETURNING id, cluster_id, desired_revision;

-- name: GetPlacementForResourceCluster :one
SELECT * FROM placements
WHERE resource_id = $1 AND cluster_id = $2;

-- name: ListPendingPlacements :many
SELECT id, desired_revision FROM placements
WHERE cluster_id = $1 AND applied_revision < desired_revision
  AND NOT apply_failed
  AND (retry_after IS NULL OR retry_after <= NOW())
ORDER BY updated_at, id;

-- name: ListClusterPlacementRevisions :many
SELECT id, desired_revision, desired_deleted, applied_revision,
       (apply_failed OR COALESCE(retry_after > NOW(), false))::boolean AS send_held
FROM placements
WHERE cluster_id = $1;

-- name: ListPlacementsByIDs :many
SELECT * FROM placements
WHERE cluster_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[])
  AND NOT apply_failed
  AND (retry_after IS NULL OR retry_after <= NOW());

-- name: MarkPlacementApplied :one
UPDATE placements
SET applied_revision = desired_revision,
    applied_at = NOW(),
    applied_error = NULL,
    retry_after = NULL,
    apply_attempts = 0,
    apply_failed = false,
    updated_at = NOW()
WHERE id = $1 AND cluster_id = $2 AND desired_revision = $3 AND NOT desired_deleted
RETURNING deployment_id;

-- name: SetPlacementApplyError :one
WITH policy AS (
    SELECT p.id,
           p.apply_attempts + CASE WHEN sqlc.arg(retrying)::boolean THEN 0 ELSE 1 END AS attempts,
           COALESCE(r.max_apply_attempts, sqlc.arg(default_max_attempts)::int) AS max_attempts,
           COALESCE(r.apply_retry_delay_seconds, sqlc.arg(default_retry_delay_seconds)::int) * INTERVAL '1 second' AS retry_delay
    FROM placements p
    LEFT JOIN resources r ON r.id = p.resource_id
    WHERE p.id = sqlc.arg(id) AND p.cluster_id = sqlc.arg(cluster_id) AND p.desired_revision = sqlc.arg(desired_revision)
)
UPDATE placements
SET applied_error = sqlc.arg(applied_error),
    apply_attempts = policy.attempts,
    apply_failed = policy.attempts >= policy.max_attempts,
    retry_after = CASE WHEN sqlc.arg(retrying)::boolean OR policy.attempts >= policy.max_attempts THEN NULL
        ELSE NOW() + policy.retry_delay END,
    updated_at = NOW()
FROM policy
WHERE placements.id = policy.id
RETURNING placements.deployment_id, placements.desired_deleted, placements.apply_failed;

-- name: DeleteAppliedPlacement :execrows
DELETE FROM placements
WHERE id = $1 AND cluster_id = $2 AND desired_revision = $3 AND desired_deleted;

-- name: AdvancePlacementPastRevision :one
UPDATE placements
SET desired_revision = sqlc.arg(observed_revision)::bigint + 1,
    applied_error = NULL,
    retry_after = NULL,
    apply_attempts = 0,
    apply_failed = false,
    updated_at = NOW()
WHERE id = sqlc.arg(id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND desired_revision = sqlc.arg(expected_revision)
  AND sqlc.arg(observed_revision)::bigint >= desired_revision
RETURNING desired_revision;

-- name: UpdatePlacementStatus :one
UPDATE placements
SET observed_revision = sqlc.arg(observed_revision),
    ready = sqlc.arg(ready),
    ready_replicas = sqlc.arg(ready_replicas),
    status_phase = sqlc.arg(status_phase),
    status_message = sqlc.arg(status_message),
    status_updated_at = NOW(),
    updated_at = NOW()
WHERE id = sqlc.arg(id)
  AND cluster_id = sqlc.arg(cluster_id)
  AND NOT desired_deleted
  AND sqlc.arg(observed_revision)::bigint >= observed_revision
  AND sqlc.arg(observed_revision)::bigint <= desired_revision
RETURNING deployment_id, desired_revision;

-- name: NotifyClusterPlacements :exec
SELECT pg_notify('placements', sqlc.arg(cluster_id)::text);

-- name: BeginClusterSync :one
UPDATE clusters
SET sync_generation = sync_generation + 1,
    connection_credential_fingerprint = $2, updated_at = NOW()
WHERE id = $1 AND removed_at IS NULL
RETURNING sync_generation;

-- name: GetClusterSyncGeneration :one
SELECT sync_generation FROM clusters WHERE id = $1 AND removed_at IS NULL;

-- name: BumpPlacementsForSecretNames :many
WITH rolled AS (
UPDATE placements
SET desired_revision = desired_revision + 1,
    applied_error = NULL,
    retry_after = NULL,
    apply_attempts = 0,
    apply_failed = false,
    updated_at = NOW()
WHERE placements.resource_id = sqlc.arg(resource_id)
  AND placements.environment_id = sqlc.arg(environment_id)
  AND NOT desired_deleted
  AND secret_names && sqlc.arg(names)::text[]
RETURNING placements.id, placements.cluster_id, placements.desired_revision, placements.deployment_id
), deploying AS (
UPDATE deployments
SET status = 'deploying', message = 'Applying environment secrets', completed_at = NULL, updated_at = NOW()
FROM rolled
WHERE deployments.id = rolled.deployment_id AND deployments.is_active
)
SELECT id, cluster_id, desired_revision FROM rolled;

-- name: ListResourceEnvironmentPlacements :many
SELECT * FROM placements
WHERE resource_id = $1 AND environment_id = $2 AND NOT desired_deleted
ORDER BY id
FOR UPDATE;
