-- name: CreatePullRegistryCredential :execrows
INSERT INTO registry_credentials (kind, cluster_id, workspace_id, secret_hash, expires_at)
SELECT 'pull', sqlc.arg(cluster_id), sqlc.arg(workspace_id), sqlc.arg(secret_hash), sqlc.arg(expires_at)
WHERE EXISTS (
    SELECT 1 FROM placement_windows pw
    WHERE pw.cluster_id = sqlc.arg(cluster_id)
      AND pw.workspace_id = sqlc.arg(workspace_id)
      AND pw.removed_at IS NULL
);

-- name: GetRegistryGrant :one
SELECT rc.kind, rc.cluster_id, rc.workspace_id, rc.build_id, b.image_repository
FROM registry_credentials rc
JOIN clusters c ON c.id = rc.cluster_id
LEFT JOIN builds b ON b.id = rc.build_id
WHERE rc.secret_hash = sqlc.arg(secret_hash)
  AND rc.kind = sqlc.arg(kind)
  AND c.removed_at IS NULL
  AND c.agent_public_key IS NOT NULL
  AND (
      (
          rc.kind = 'pull'
          AND rc.expires_at > NOW()
          AND EXISTS (
              SELECT 1 FROM placement_windows pw
              WHERE pw.cluster_id = rc.cluster_id
                AND pw.workspace_id = rc.workspace_id
                AND pw.removed_at IS NULL
          )
      )
      OR (
          rc.kind = 'build'
          AND b.status IN ('queued', 'running')
          AND b.cluster_id = rc.cluster_id
      )
  );

-- name: DeleteExpiredPullRegistryCredentials :execrows
DELETE FROM registry_credentials
WHERE kind = 'pull' AND expires_at <= NOW();

-- name: DeleteStoppedBuildRegistryCredentials :execrows
DELETE FROM registry_credentials rc
WHERE rc.kind = 'build'
  AND NOT EXISTS (
      SELECT 1 FROM builds b
      WHERE b.id = rc.build_id
        AND b.status IN ('queued', 'running')
        AND b.cluster_id = rc.cluster_id
  );

-- name: DeleteClusterRegistryCredentials :exec
DELETE FROM registry_credentials WHERE cluster_id = $1;

-- name: CreateBuildRegistryCredential :execrows
INSERT INTO registry_credentials (kind, cluster_id, workspace_id, build_id, secret_hash)
SELECT 'build', b.cluster_id, r.workspace_id, b.id, sqlc.arg(secret_hash)
FROM builds b
JOIN resources r ON r.id = b.resource_id
WHERE b.id = sqlc.arg(build_id)
  AND b.cluster_id = sqlc.arg(cluster_id)::uuid
  AND b.status = 'queued';
