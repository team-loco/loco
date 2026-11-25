-- Cluster queries for agent operations

-- name: UpdateClusterAgentInfo :exec
UPDATE clusters
SET agent_version = $2,
    capacity_cpu_millicores = $3,
    capacity_memory_bytes = $4,
    role = $5,
    domains = sqlc.arg(domains)::text[],
    platforms = sqlc.arg(platforms)::text[],
    updated_at = NOW()
WHERE id = $1;

-- name: RecordClusterHeartbeat :exec
INSERT INTO cluster_heartbeats (cluster_id, last_heartbeat, health_status)
SELECT id, sqlc.arg(last_heartbeat)::timestamptz, sqlc.arg(health_status)::text
FROM clusters WHERE id = sqlc.arg(id) AND removed_at IS NULL
FOR KEY SHARE
ON CONFLICT (cluster_id) DO UPDATE
SET last_heartbeat = EXCLUDED.last_heartbeat, health_status = EXCLUDED.health_status;

-- name: UpdateClusterReport :exec
-- Matches only a changed report from the current stream; an unchanged one takes no lock on the cluster.
UPDATE clusters
SET capacity_cpu_millicores = sqlc.arg(capacity_cpu_millicores)::bigint,
    capacity_memory_bytes = sqlc.arg(capacity_memory_bytes)::bigint,
    role = sqlc.arg(role)::cluster_role,
    domains = sqlc.arg(domains)::text[],
    platforms = sqlc.arg(platforms)::text[],
    updated_at = NOW()
WHERE id = sqlc.arg(id) AND removed_at IS NULL AND sync_generation = sqlc.arg(generation)
  AND (capacity_cpu_millicores, capacity_memory_bytes, role, domains, platforms) IS DISTINCT FROM
      (sqlc.arg(capacity_cpu_millicores)::bigint, sqlc.arg(capacity_memory_bytes)::bigint,
       sqlc.arg(role)::cluster_role, sqlc.arg(domains)::text[], sqlc.arg(platforms)::text[]);

-- name: DeleteClusterHeartbeat :exec
DELETE FROM cluster_heartbeats WHERE cluster_id = $1;

-- name: GetClusterRole :one
SELECT role FROM clusters WHERE id = $1;

-- name: GetClusterByID :one
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status,
       last_health_check, last_heartbeat, capacity_cpu_millicores,
       capacity_memory_bytes, agent_version, created_at, updated_at
FROM clusters LEFT JOIN cluster_heartbeats ON cluster_id = id
WHERE id = $1;

-- name: CreateCluster :exec
INSERT INTO clusters (name, region, provider, is_active, is_default, id)
VALUES (sqlc.arg(name), sqlc.arg(region), 'kubernetes', true, false,
        sqlc.arg(id))
ON CONFLICT (id) DO NOTHING;

-- name: LockClusterRegistration :one
SELECT id, name, region, removed_at, agent_public_key
FROM clusters WHERE id = $1 FOR UPDATE;

-- name: SetClusterJoinToken :one
UPDATE clusters
SET join_token_hash = sqlc.arg(join_token_hash),
    join_token_expires_at = sqlc.arg(join_token_expires_at),
    join_token_used_at = NULL,
    updated_at = NOW()
WHERE id = sqlc.arg(id) AND removed_at IS NULL
RETURNING id;

-- name: EnrollCluster :one
UPDATE clusters
SET agent_public_key = sqlc.arg(public_key),
    cluster_key = sqlc.arg(cluster_key),
    join_token_used_at = NOW(),
    sync_generation = sync_generation + 1,
    updated_at = NOW()
WHERE join_token_hash = sqlc.arg(join_token_hash)
  AND join_token_expires_at > NOW()
  AND removed_at IS NULL
  AND (cluster_key IS NULL OR cluster_key = sqlc.arg(cluster_key))
  AND (join_token_used_at IS NULL OR agent_public_key = sqlc.arg(public_key))
RETURNING id, name, region, cluster_key;

-- name: GetEnrolledCluster :one
SELECT id, name, region, cluster_key, agent_public_key FROM clusters
WHERE id = $1 AND removed_at IS NULL AND agent_public_key IS NOT NULL;

-- name: GetEnrolledClusterByKey :one
SELECT id, name, region, cluster_key, agent_public_key FROM clusters
WHERE cluster_key = $1 AND removed_at IS NULL AND agent_public_key IS NOT NULL;

-- name: RecordClusterAssertion :execrows
INSERT INTO cluster_assertions (cluster_id, jti, expires_at)
VALUES (sqlc.arg(cluster_id), sqlc.arg(jti), sqlc.arg(expires_at))
ON CONFLICT (cluster_id, jti) DO NOTHING;

-- name: DeleteExpiredClusterAssertions :exec
DELETE FROM cluster_assertions WHERE expires_at < NOW();

-- name: ListAdminClusters :many
SELECT id, cluster_key, name, region, is_active, removed_at, health_status,
       last_heartbeat, agent_version, agent_public_key, role, domains
FROM clusters LEFT JOIN cluster_heartbeats ON cluster_id = id
ORDER BY created_at, id;

-- name: LockClusterLifecycle :one
SELECT id, is_active, removed_at FROM clusters WHERE id = $1 FOR UPDATE;

-- name: LockAssignableCluster :one
SELECT id, role, platforms FROM clusters
WHERE id = $1 AND is_active = true AND removed_at IS NULL FOR SHARE;

-- name: UpdateClusterLifecycle :exec
UPDATE clusters
SET is_active = sqlc.arg(active),
    removed_at = CASE WHEN sqlc.arg(remove_cluster)::boolean THEN NOW()
                      WHEN sqlc.arg(restore_cluster)::boolean THEN NULL ELSE removed_at END,
    join_token_hash = CASE WHEN sqlc.arg(remove_cluster)::boolean THEN NULL ELSE join_token_hash END,
    sync_generation = sync_generation + CASE
        WHEN sqlc.arg(remove_cluster)::boolean OR sqlc.arg(restore_cluster)::boolean THEN 1 ELSE 0 END,
    updated_at = NOW()
WHERE id = sqlc.arg(id);

-- name: ListClusterWorkloads :many
SELECT p.id, 'placement'::text AS kind FROM placements p
WHERE p.cluster_id = $1
UNION ALL
SELECT l.deployment_id, 'deployment'::text AS kind FROM live_deployment_placements l
WHERE l.cluster_id = $1
UNION ALL
SELECT b.id, 'build'::text AS kind FROM builds b
WHERE b.cluster_id = $1 AND b.status IN ('queued', 'running')
ORDER BY kind, id;

-- name: ListClusterDomains :many
SELECT id, name, region, domains FROM clusters
WHERE id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY region, name;
