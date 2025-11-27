-- name: CreateOrg :one
INSERT INTO organizations (name, created_by)
VALUES ($1, $2)
RETURNING *;

-- name: GetOrgByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: GetOrgByName :one
SELECT * FROM organizations WHERE name = $1;

-- name: ListOrgsForUser :many
SELECT DISTINCT o.*
FROM organizations o
JOIN organization_members om ON om.organization_id = o.id
WHERE om.user_id = $1
ORDER BY o.created_at DESC
OFFSET $2
LIMIT $3;

-- name: CountOrgsForUser :one
SELECT COUNT(*) FROM organization_members WHERE user_id = $1;

-- name: UpdateOrgName :one
UPDATE organizations
SET name = $2, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteOrg :exec
DELETE FROM organizations WHERE id = $1;

-- name: ListWorkspacesForOrg :many
SELECT id, name, created_by, created_at
FROM workspaces
WHERE org_id = $1
ORDER BY created_at DESC;

-- name: OrgHasWorkspacesWithApps :one
SELECT EXISTS(
  SELECT 1 FROM workspaces w
  WHERE w.org_id = $1
  AND EXISTS(
    SELECT 1 FROM apps a WHERE a.workspace_id = w.id
  )
) as has_apps_in_workspaces;

-- name: DeleteEmptyWorkspacesForOrg :exec
DELETE FROM workspaces
WHERE org_id = $1
AND NOT EXISTS (
  SELECT 1 FROM apps WHERE workspace_id = workspaces.id
);

-- name: IsOrgNameUnique :one
SELECT COUNT(*) = 0 as is_unique
FROM organizations
WHERE name = $1;

-- name: IsOrgMember :one
SELECT EXISTS(
  SELECT 1 FROM organization_members
  WHERE organization_id = $1 AND user_id = $2
) as is_member;

-- name: AddOrgMember :exec
INSERT INTO organization_members (organization_id, user_id)
VALUES ($1, $2);
