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
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateClusterHeartbeat :exec
UPDATE clusters
SET last_heartbeat = $2,
    capacity_cpu_millicores = $3,
    capacity_memory_bytes = $4,
    health_status = $5,
    updated_at = NOW()
WHERE id = $1;

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

-- name: GetClustersByWorkspaceDeployments :many
SELECT DISTINCT c.id, c.name, c.region, c.observability_proxy_endpoint
FROM clusters c
INNER JOIN deployments d ON d.cluster_id = c.id
INNER JOIN resources r ON r.id = d.resource_id
WHERE r.workspace_id = $1
  AND c.is_active = true
  AND d.is_active = true;

-- name: SetClusterObservabilityEndpoint :exec
UPDATE clusters
SET observability_proxy_endpoint = $2, updated_at = NOW()
WHERE id = $1;

-- name: SetClusterGatewayHostname :exec
UPDATE clusters
SET gateway_hostname = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetFailoverPeersForResource :many
-- Regions other than the current one where this resource has an active deployment on a
-- healthy cluster that advertises a gateway hostname. These become Envoy priority-1
-- fallback backends. Clusters without a gateway_hostname are excluded: they cannot be
-- addressed by a peer region's Envoy.
SELECT DISTINCT ON (c.region)
       c.region,
       c.gateway_hostname
FROM clusters c
INNER JOIN deployments d ON d.cluster_id = c.id
WHERE d.resource_id = $1
  AND d.is_active = true
  AND c.region <> sqlc.arg(exclude_region)::text
  AND c.is_active = true
  AND c.gateway_hostname IS NOT NULL
  AND c.gateway_hostname <> ''
  AND (c.health_status IS NULL OR c.health_status = 'healthy')
ORDER BY c.region;
