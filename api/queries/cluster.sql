-- Cluster queries for agent operations

-- name: UpdateClusterAgentInfo :exec
UPDATE clusters
SET agent_version = $2,
    role = sqlc.arg(role),
    domains = sqlc.arg(domains)::text[],
    platforms = CASE WHEN sqlc.arg(has_capabilities)::boolean THEN sqlc.arg(platforms)::text[] ELSE platforms END,
    updated_at = NOW()
WHERE id = $1;

-- name: CreateClusterHeartbeat :one
-- Inserts the cluster's first heartbeat and reports created. A concurrent first heartbeat waits for this one's
-- commit. Returns nothing for a removed cluster or a superseded stream; the share lock holds off a new stream
-- until the heartbeat commits.
WITH target AS (
    SELECT id, name, region, is_active FROM clusters
    WHERE id = sqlc.arg(id) AND removed_at IS NULL AND sync_generation = sqlc.arg(generation)
    FOR SHARE
), inserted AS (
    INSERT INTO cluster_heartbeats (cluster_id, last_heartbeat, health_status)
    SELECT id, clock_timestamp(), sqlc.arg(health_status)::text FROM target
    ON CONFLICT (cluster_id) DO NOTHING
    RETURNING cluster_id
)
SELECT target.name, target.region, target.is_active, EXISTS (SELECT 1 FROM inserted)::boolean AS created
FROM target;

-- name: LockClusterHeartbeat :one
SELECT health_status, last_heartbeat, silent_since FROM cluster_heartbeats WHERE cluster_id = $1 FOR UPDATE;

-- name: RecordClusterHeartbeat :one
-- Stamps the heartbeat with the database clock under the row lock, so the time never moves backwards, and ends
-- any silence mark.
UPDATE cluster_heartbeats
SET last_heartbeat = GREATEST(last_heartbeat, clock_timestamp()),
    health_status = sqlc.arg(health_status)::text,
    silent_since = NULL
WHERE cluster_id = sqlc.arg(id)
RETURNING last_heartbeat;

-- name: MarkSilentClusters :many
-- Marks each active cluster whose last heartbeat is older than silent_after_seconds on the database clock, the clock
-- heartbeats are stamped with, and returns it once. The cluster rows
-- are share-locked first, so a pause or removal either commits before and excludes the cluster, or waits for this
-- transaction.
WITH due AS (
    SELECT c.id, c.name, c.region FROM clusters c JOIN cluster_heartbeats h ON h.cluster_id = c.id
    WHERE c.removed_at IS NULL AND c.is_active
      AND h.silent_since IS NULL
      AND h.last_heartbeat < clock_timestamp() - make_interval(secs => sqlc.arg(silent_after_seconds)::float8)
    ORDER BY c.id
    FOR SHARE OF c
)
UPDATE cluster_heartbeats h
SET silent_since = h.last_heartbeat
FROM due
WHERE h.cluster_id = due.id AND h.silent_since IS NULL
  AND h.last_heartbeat < clock_timestamp() - make_interval(secs => sqlc.arg(silent_after_seconds)::float8)
RETURNING due.id, due.name, due.region;

-- name: UpdateClusterReport :exec
-- Matches only a changed report from the current stream; an unchanged one takes no lock on the cluster.
UPDATE clusters
SET role = sqlc.arg(role)::cluster_role,
    domains = sqlc.arg(domains)::text[],
    platforms = CASE WHEN sqlc.arg(has_capabilities)::boolean THEN sqlc.arg(platforms)::text[] ELSE platforms END,
    updated_at = NOW()
WHERE id = sqlc.arg(id) AND removed_at IS NULL AND sync_generation = sqlc.arg(generation)
  AND (role, domains, platforms) IS DISTINCT FROM
      (sqlc.arg(role)::cluster_role, sqlc.arg(domains)::text[],
       CASE WHEN sqlc.arg(has_capabilities)::boolean THEN sqlc.arg(platforms)::text[] ELSE platforms END);

-- name: DeleteClusterHeartbeat :exec
DELETE FROM cluster_heartbeats WHERE cluster_id = $1;

-- name: GetClusterRole :one
SELECT role FROM clusters WHERE id = $1;

-- name: GetClusterByID :one
SELECT id, name, region, provider, is_active, is_default, endpoint, health_status,
       last_health_check, last_heartbeat, agent_version, created_at, updated_at
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
       last_heartbeat, silent_since, agent_version, agent_public_key, role, domains,
       h.health_message
FROM clusters LEFT JOIN cluster_heartbeats h ON cluster_id = id
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

-- name: RecordClusterHealthMessage :exec
UPDATE cluster_heartbeats SET health_message=sqlc.arg(health_message)
WHERE cluster_id=sqlc.arg(id);

-- name: ListClusterAttention :many
SELECT l.deployment_id AS id, 'deployment'::text AS kind, l.resource_id, r.name AS resource_name,
       w.org_id, r.workspace_id, l.environment_id, e.slug AS environment_slug,
       CASE WHEN l.status = 'running' AND cardinality(rr.unserved_domains) > 0 THEN 'degraded' ELSE l.status::text END AS status,
       COALESCE(NULLIF(rr.problem->>'detail', ''), p.applied_error, '')::text AS message,
       COALESCE(rr.problem->>'reason', p.problem_reason, '')::text AS problem_reason,
       COALESCE(rr.unserved_domains, ARRAY[]::text[]) AS unserved_domains, COALESCE(p.status_updated_at, d.created_at)::timestamptz AS updated_at
FROM live_deployment_placements l
JOIN deployments d ON d.id = l.deployment_id
JOIN resources r ON r.id = l.resource_id
JOIN workspaces w ON w.id = r.workspace_id
JOIN environments e ON e.id = l.environment_id
LEFT JOIN placements p ON p.deployment_id = l.deployment_id AND p.cluster_id = l.cluster_id
LEFT JOIN resource_region_inputs rr ON rr.deployment_id = l.deployment_id AND rr.cluster_id = l.cluster_id
WHERE l.cluster_id = sqlc.arg(cluster_id)
  AND (l.status IN ('failed', 'crashed', 'degraded') OR p.problem_reason <> '' OR p.apply_failed OR cardinality(rr.unserved_domains) > 0 OR (rr.problem IS NOT NULL AND rr.problem <> 'null'::jsonb))
UNION ALL
SELECT b.id, 'build'::text, b.resource_id, r.name, w.org_id, r.workspace_id, COALESCE(b.origin_environment_id, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(e.slug, '')::text, b.status::text, b.message, ''::text, ARRAY[]::text[], COALESCE(b.finished_at, b.started_at, b.created_at)::timestamptz
FROM builds b
JOIN resources r ON r.id = b.resource_id
JOIN workspaces w ON w.id = r.workspace_id
LEFT JOIN environments e ON e.id = b.origin_environment_id
WHERE b.cluster_id = sqlc.arg(cluster_id) AND b.status = 'failed'
  AND NOT EXISTS (SELECT 1 FROM builds newer WHERE newer.resource_id = b.resource_id
                  AND newer.origin_environment_id IS NOT DISTINCT FROM b.origin_environment_id AND (newer.created_at, newer.id) > (b.created_at, b.id))
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListClusterEvents :many
-- Each related subject contributes at most one page through its own index, so a quiet cluster never
-- scans the installation's globally ordered event history. UNION removes events linked in several ways.
WITH related AS (
    (SELECT es.seq FROM events es
     WHERE es.subject_type = 'cluster' AND es.subject_id = sqlc.arg(cluster_id)
     ORDER BY es.seq DESC LIMIT 20)
    UNION
    SELECT recent.seq FROM deployment_placements dp
    CROSS JOIN LATERAL (
        SELECT ed.seq FROM events ed WHERE ed.deployment_id = dp.deployment_id ORDER BY ed.seq DESC LIMIT 20
    ) recent
    WHERE dp.cluster_id = sqlc.arg(cluster_id)
    UNION
    SELECT recent.seq FROM builds b
    CROSS JOIN LATERAL (
        SELECT eb.seq FROM events eb WHERE eb.subject_type = 'build' AND eb.subject_id = b.id ORDER BY eb.seq DESC LIMIT 20
    ) recent
    WHERE b.cluster_id = sqlc.arg(cluster_id)
), latest AS (
    SELECT seq FROM related ORDER BY seq DESC LIMIT 20
)
SELECT sqlc.embed(e), event_title(e.type, e.data)::text AS title, event_severity(e.type)::text AS severity,
       COALESCE(u.name, e.actor_token_name, '')::text AS actor_name, COALESCE(u.email, '')::text AS actor_email
FROM latest JOIN events e ON e.seq = latest.seq
LEFT JOIN users u ON e.actor_type = 'user' AND u.id = e.actor_id
ORDER BY e.seq DESC;
