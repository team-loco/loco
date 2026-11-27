-- name: ListAllocationWorkspaces :many
SELECT w.id, w.name, w.org_id, o.name AS org_name, count(r.id)::int AS resource_count
FROM workspaces w
JOIN organizations o ON o.id = w.org_id
LEFT JOIN resources r ON r.workspace_id = w.id
WHERE (sqlc.narg('org_id')::uuid IS NULL OR w.org_id = sqlc.narg('org_id'))
  AND (sqlc.narg('ids')::uuid[] IS NULL OR w.id = ANY(sqlc.narg('ids')))
GROUP BY w.id, o.name;

-- name: ListAllocationResources :many
SELECT r.id, r.name, r.workspace_id, w.org_id
FROM resources r JOIN workspaces w ON w.id = r.workspace_id
WHERE (sqlc.narg('workspace_id')::uuid IS NULL OR r.workspace_id = sqlc.narg('workspace_id'))
  AND (sqlc.narg('ids')::uuid[] IS NULL OR r.id = ANY(sqlc.narg('ids')));
