-- name: UpsertPlacement :one
INSERT INTO placements (resource_id, cluster_id, region, deployment_id, desired_spec)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (resource_id, cluster_id) DO UPDATE
SET region = EXCLUDED.region,
    deployment_id = EXCLUDED.deployment_id,
    desired_spec = EXCLUDED.desired_spec,
    desired_deleted = false,
    desired_revision = placements.desired_revision + 1,
    applied_error = NULL,
    updated_at = NOW()
RETURNING id, desired_revision;

-- name: MarkPlacementDeleted :many
UPDATE placements
SET desired_deleted = true,
    desired_spec = NULL,
    desired_revision = desired_revision + 1,
    applied_error = NULL,
    updated_at = NOW()
WHERE resource_id = $1 AND cluster_id = $2 AND NOT desired_deleted
RETURNING id, cluster_id, desired_revision;

-- name: MarkResourcePlacementsDeleted :many
UPDATE placements
SET desired_deleted = true,
    desired_spec = NULL,
    desired_revision = desired_revision + 1,
    applied_error = NULL,
    updated_at = NOW()
WHERE resource_id = $1 AND NOT desired_deleted
RETURNING id, cluster_id, desired_revision;

-- name: GetPlacementForResourceCluster :one
SELECT * FROM placements
WHERE resource_id = $1 AND cluster_id = $2;

-- name: ListPendingPlacements :many
SELECT * FROM placements
WHERE cluster_id = $1 AND applied_revision < desired_revision
ORDER BY updated_at, id;

-- name: ListClusterPlacementRevisions :many
SELECT id, desired_revision, desired_deleted, applied_revision
FROM placements
WHERE cluster_id = $1;

-- name: ListPlacementsByIDs :many
SELECT * FROM placements
WHERE cluster_id = $1 AND id = ANY(sqlc.arg(ids)::uuid[]);

-- name: MarkPlacementApplied :one
UPDATE placements
SET applied_revision = desired_revision,
    applied_at = NOW(),
    applied_error = NULL,
    updated_at = NOW()
WHERE id = $1 AND cluster_id = $2 AND desired_revision = $3 AND NOT desired_deleted
RETURNING deployment_id;

-- name: SetPlacementApplyError :one
UPDATE placements
SET applied_error = $4,
    updated_at = NOW()
WHERE id = $1 AND cluster_id = $2 AND desired_revision = $3
RETURNING deployment_id, desired_deleted;

-- name: DeleteAppliedPlacement :execrows
DELETE FROM placements
WHERE id = $1 AND cluster_id = $2 AND desired_revision = $3 AND desired_deleted;

-- name: AdvancePlacementPastRevision :one
UPDATE placements
SET desired_revision = sqlc.arg(observed_revision)::bigint + 1,
    applied_error = NULL,
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
SET sync_generation = sync_generation + 1, updated_at = NOW()
WHERE id = $1
RETURNING sync_generation;

-- name: GetClusterSyncGeneration :one
SELECT sync_generation FROM clusters WHERE id = $1;
