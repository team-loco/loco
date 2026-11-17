-- name: ListAccountDeletionBlockingOrganizations :many
-- Keep the active-successor predicate aligned with OrgAdminHasNoActiveSuccessor.
SELECT o.id, o.name
FROM organizations o
JOIN user_scopes s ON s.entity_type = 'organization' AND s.entity_id = o.id
JOIN users u ON u.id = s.user_id
WHERE s.user_id = sqlc.arg('user_id') AND s.scope = 'admin' AND u.status = 'active'
  AND NOT EXISTS (
    SELECT 1 FROM user_scopes other JOIN users other_user ON other_user.id = other.user_id
    WHERE other.entity_type = 'organization' AND other.entity_id = o.id
      AND other.scope = 'admin' AND other.user_id <> s.user_id AND other_user.status = 'active'
  )
ORDER BY o.id
LIMIT sqlc.arg('page_size');

-- name: ListAccountDeletionTokens :many
SELECT t.id, t.name, t.entity_type, t.entity_id, t.created_at,
       t.expires_at, t.display_suffix, COALESCE(o.name, w.name, r.name, '')::text AS owner_name
FROM api_tokens t
LEFT JOIN organizations o ON t.entity_type = 'organization' AND o.id = t.entity_id
LEFT JOIN workspaces w ON (t.entity_type = 'workspace' AND w.id = t.entity_id)
LEFT JOIN resources r ON t.entity_type = 'resource' AND r.id = t.entity_id
LEFT JOIN workspaces rw ON rw.id = r.workspace_id
WHERE t.created_by = sqlc.arg('user_id')
  AND t.entity_type IN ('organization', 'workspace', 'resource')
  AND t.expires_at > NOW()
  AND (sqlc.arg('system_read')::boolean
    OR COALESCE(o.id, w.org_id, rw.org_id) = ANY(sqlc.arg('organization_ids')::uuid[])
    OR COALESCE(w.id, rw.id) = ANY(sqlc.arg('workspace_ids')::uuid[])
    OR r.id = ANY(sqlc.arg('resource_ids')::uuid[]))
  AND (sqlc.narg('page_token')::text IS NULL OR (t.created_at, t.id) < (
    sqlc.narg('page_created_at')::timestamptz, sqlc.narg('page_token')::uuid))
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg('page_size');

-- name: HasInaccessibleAccountDeletionTokens :one
SELECT EXISTS (SELECT 1
FROM api_tokens t
LEFT JOIN organizations o ON t.entity_type = 'organization' AND o.id = t.entity_id
LEFT JOIN workspaces w ON (t.entity_type = 'workspace' AND w.id = t.entity_id)
LEFT JOIN resources r ON t.entity_type = 'resource' AND r.id = t.entity_id
LEFT JOIN workspaces rw ON rw.id = r.workspace_id
WHERE t.created_by = sqlc.arg('user_id')
  AND t.entity_type IN ('organization', 'workspace', 'resource')
  AND t.expires_at > NOW()
  AND NOT COALESCE((sqlc.arg('system_read')::boolean
    OR COALESCE(o.id, w.org_id, rw.org_id) = ANY(sqlc.arg('organization_ids')::uuid[])
    OR COALESCE(w.id, rw.id) = ANY(sqlc.arg('workspace_ids')::uuid[])
    OR r.id = ANY(sqlc.arg('resource_ids')::uuid[])), FALSE)) AS has_inaccessible;
