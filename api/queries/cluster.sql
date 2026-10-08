-- Cluster queries for agent operations

-- name: GetClusterByAgentToken :one
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status,
       last_health_check, agent_token_hash, last_heartbeat, capacity_cpu_millicores,
       capacity_memory_bytes, agent_version, created_at, updated_at
FROM clusters
WHERE agent_token_hash = $1;

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
WHERE id = $1;

-- name: GetClusterBuildsEnabled :one
SELECT builds_enabled FROM clusters WHERE id = $1;

-- name: SetClusterAgentToken :exec
UPDATE clusters
SET agent_token_hash = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetClusterByID :one
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status,
       last_health_check, agent_token_hash, last_heartbeat, capacity_cpu_millicores,
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
