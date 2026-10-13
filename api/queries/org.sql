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
         (SELECT created_at FROM organizations WHERE id = sqlc.narg('page_token')::uuid),
         sqlc.narg('page_token')::uuid
       ))
ORDER BY o.created_at DESC, o.id DESC
LIMIT $2;

-- name: UpdateOrgName :one
UPDATE organizations
SET name = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

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
