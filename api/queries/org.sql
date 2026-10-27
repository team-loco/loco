-- name: CreateOrg :one
INSERT INTO organizations (name, created_by)
VALUES ($1, $2)
RETURNING *;

-- name: GetOrgByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: GetOrgByName :one
SELECT * FROM organizations WHERE name = $1;

-- name: ListOrgsForUser :many
SELECT o.*
FROM organizations o
WHERE EXISTS (
    SELECT 1 FROM user_scopes us
    WHERE us.user_id = $1
      AND us.entity_type = 'organization'
      AND us.entity_id = o.id
  )
  AND (sqlc.narg('page_token')::text IS NULL
       OR (o.created_at, o.id) < (
         sqlc.narg('page_created_at')::timestamptz,
         sqlc.narg('page_token')::uuid
       ))
ORDER BY o.created_at DESC, o.id DESC
LIMIT $2;

-- name: UpdateOrgName :one
UPDATE organizations
SET name = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: LockOrgForDeletion :exec
SELECT id FROM organizations WHERE id = $1 FOR UPDATE;

-- name: LockOrgWorkspacesForDeletion :exec
SELECT id FROM workspaces WHERE org_id = $1 ORDER BY id FOR UPDATE;

-- name: DeleteOrg :exec
DELETE FROM organizations WHERE id = $1;

-- name: OrgHasWorkspacesWithResources :one
SELECT EXISTS(
  SELECT 1 FROM workspaces w
  WHERE w.org_id = $1
  AND EXISTS(
    SELECT 1 FROM resources r WHERE r.workspace_id = w.id
  )
) as has_resources_in_workspaces;

-- name: IsOrgNameUnique :one
SELECT COUNT(*) = 0 as is_unique
FROM organizations
WHERE name = $1
AND (sqlc.narg('exclude_id')::uuid IS NULL OR id != sqlc.narg('exclude_id')::uuid);

-- name: ListOrgMembersWithDetails :many
WITH member_scopes AS (
  SELECT us.user_id, array_agg(us.scope ORDER BY us.scope)::text[] AS scopes
  FROM user_scopes us
  WHERE us.entity_type = 'organization' AND us.entity_id = $1
  GROUP BY us.user_id
)
SELECT u.id, u.email, u.name, u.avatar_url, ms.scopes
FROM member_scopes ms
JOIN users u ON u.id = ms.user_id
WHERE (sqlc.narg('page_token')::text IS NULL
       OR u.id < sqlc.narg('page_token')::uuid)
ORDER BY u.id DESC
LIMIT $2;

-- name: IsOrgMember :one
SELECT EXISTS (
  SELECT 1 FROM user_scopes us
  WHERE us.user_id = sqlc.arg('user_id')
    AND us.entity_type = 'organization'
    AND us.entity_id = sqlc.arg('org_id')
) AS is_member;

-- name: LockUserForMembership :one
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: RemoveOrgMember :exec
DELETE FROM user_scopes us
WHERE us.user_id = sqlc.arg('user_id')
  AND (
    (us.entity_type = 'organization' AND us.entity_id = sqlc.arg('org_id'))
    OR (us.entity_type = 'workspace' AND us.entity_id IN (
      SELECT w.id FROM workspaces w WHERE w.org_id = sqlc.arg('org_id')
    ))
    OR (us.entity_type = 'resource' AND us.entity_id IN (
      SELECT r.id FROM resources r JOIN workspaces w ON w.id = r.workspace_id WHERE w.org_id = sqlc.arg('org_id')
    ))
  );

-- name: LockOrgForSuspension :one
SELECT * FROM organizations WHERE id = $1 FOR UPDATE;

-- name: SuspendOrg :one
UPDATE organizations
SET suspended_at = NOW(), suspension_reason = sqlc.arg(reason), updated_at = NOW()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ResumeOrg :one
UPDATE organizations
SET suspended_at = NULL, suspension_reason = NULL, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: LockWorkspaceOrganization :exec
SELECT o.id
FROM organizations o
JOIN workspaces w ON w.org_id = o.id
WHERE w.id = $1
FOR SHARE OF o;

-- name: LockResourceOrganization :one
SELECT o.suspension_reason
FROM organizations o
JOIN workspaces w ON w.org_id = o.id
JOIN resources r ON r.workspace_id = w.id
WHERE r.id = $1
FOR SHARE OF o;
