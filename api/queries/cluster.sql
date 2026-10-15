-- Cluster queries for agent operations

-- name: UpdateClusterAgentInfo :exec
UPDATE clusters
SET agent_version = $2,
    capacity_cpu_millicores = $3,
    capacity_memory_bytes = $4,
    builds_enabled = $5,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateClusterHeartbeat :exec
UPDATE clusters
SET last_heartbeat = $2,
    capacity_cpu_millicores = $3,
    capacity_memory_bytes = $4,
    health_status = $5,
    builds_enabled = $6,
    updated_at = NOW()
WHERE id = $1 AND removed_at IS NULL;

-- name: GetClusterBuildsEnabled :one
SELECT builds_enabled FROM clusters WHERE id = $1;

-- name: GetClusterByID :one
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status,
       last_health_check, last_heartbeat, capacity_cpu_millicores,
       capacity_memory_bytes, agent_version, created_at, updated_at
FROM clusters
WHERE id = $1;

-- name: GetObservabilityClustersForWorkspace :many
SELECT c.id, c.name, c.region, c.observability_proxy_endpoint
FROM clusters c
WHERE c.is_active = true
  AND (
    EXISTS (
      SELECT 1
      FROM deployments d
      INNER JOIN resources r ON r.id = d.resource_id
      WHERE d.cluster_id = c.id
        AND d.is_active = true
        AND r.workspace_id = $1
    )
    OR EXISTS (
      SELECT 1
      FROM builds b
      INNER JOIN resources r ON r.id = b.resource_id
      WHERE b.cluster_id = c.id
        AND r.workspace_id = $1
    )
  )
ORDER BY c.region, c.id;

-- name: SetClusterObservabilityEndpoint :exec
UPDATE clusters
SET observability_proxy_endpoint = $2, updated_at = NOW()
WHERE id = $1;

-- name: SetClusterGatewayHostname :exec
UPDATE clusters
SET gateway_hostname = $2, updated_at = NOW()
WHERE id = $1;

-- name: AttachCluster :one
INSERT INTO clusters (cluster_key, name, region, tier, provider, is_active, is_default)
VALUES ($1, $2, $3, $4, 'kubernetes', true, false)
ON CONFLICT (cluster_key) DO UPDATE
SET name = EXCLUDED.name, updated_at = NOW()
WHERE clusters.region = EXCLUDED.region AND clusters.tier = EXCLUDED.tier
  AND clusters.removed_at IS NULL
RETURNING id, name, region;

-- name: GetClusterByKey :one
SELECT id, name, region FROM clusters
WHERE cluster_key = $1 AND removed_at IS NULL;

-- name: ListAdminClusters :many
SELECT id, cluster_key, name, region, tier, is_active, removed_at, health_status,
       last_heartbeat, agent_version, connection_credential_fingerprint
FROM clusters ORDER BY created_at, id;

-- name: LockClusterLifecycle :one
SELECT id, is_active, removed_at FROM clusters WHERE id = $1 FOR UPDATE;

-- name: LockAssignableCluster :one
SELECT id FROM clusters WHERE id = $1 AND is_active = true AND removed_at IS NULL FOR SHARE;

-- name: UpdateClusterLifecycle :exec
UPDATE clusters
SET is_active = sqlc.arg(active),
    removed_at = CASE WHEN sqlc.arg(remove_cluster)::boolean THEN NOW()
                      WHEN sqlc.arg(restore_cluster)::boolean THEN NULL ELSE removed_at END,
    last_heartbeat = CASE WHEN sqlc.arg(restore_cluster)::boolean THEN NULL ELSE last_heartbeat END,
    health_status = CASE WHEN sqlc.arg(restore_cluster)::boolean THEN NULL ELSE health_status END,
    sync_generation = sync_generation + CASE
        WHEN sqlc.arg(remove_cluster)::boolean OR sqlc.arg(restore_cluster)::boolean THEN 1 ELSE 0 END,
    updated_at = NOW()
WHERE id = sqlc.arg(id);

-- name: ListClusterWorkloads :many
SELECT p.id, 'placement'::text AS kind FROM placements p
WHERE p.cluster_id = $1
UNION ALL
SELECT d.id, 'deployment'::text AS kind FROM deployments d
WHERE d.cluster_id = $1 AND d.is_active = true
UNION ALL
SELECT b.id, 'build'::text AS kind FROM builds b
WHERE b.cluster_id = $1 AND b.status IN ('queued', 'running')
ORDER BY kind, id;
