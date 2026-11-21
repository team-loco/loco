-- Regions are the region codes on cluster rows.

-- name: NextRegionNumber :one
-- One more than the highest n of the clusters in {prefix}-{n} regions.
SELECT (COALESCE(MAX(substring(region FROM '[0-9]+$')::int), 0) + 1)::int
FROM clusters WHERE region ~ ('^' || sqlc.arg(prefix)::text || '-[0-9]+$');

-- name: RegionHasClusters :one
SELECT EXISTS (SELECT 1 FROM clusters WHERE region = $1)::boolean;

-- name: IsLiveRegion :one
SELECT EXISTS (SELECT 1 FROM clusters WHERE region = $1 AND removed_at IS NULL)::boolean;

-- name: ListAdminRegions :many
SELECT region AS code, (count(*) FILTER (WHERE removed_at IS NULL))::int AS cluster_count
FROM clusters GROUP BY region ORDER BY region;
