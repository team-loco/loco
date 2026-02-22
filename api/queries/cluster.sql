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
