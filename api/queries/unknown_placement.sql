-- name: RecordUnknownPlacements :many
WITH recorded AS (
    INSERT INTO cluster_unknown_placements (cluster_id, placement_id, reported_revision)
    SELECT sqlc.arg('cluster_id'), inventory.id, inventory.revision
    FROM (
        SELECT unnest(sqlc.arg('placement_ids')::uuid[]) AS id,
               unnest(sqlc.arg('revisions')::bigint[]) AS revision
    ) inventory
    WHERE NOT EXISTS (
        SELECT 1 FROM placements p
        WHERE p.cluster_id = sqlc.arg('cluster_id') AND p.id = inventory.id
    )
    ON CONFLICT (cluster_id, placement_id) DO NOTHING
    RETURNING placement_id, reported_revision, first_seen_at
)
SELECT placement_id, reported_revision, first_seen_at FROM recorded ORDER BY placement_id;
