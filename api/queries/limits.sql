-- name: GetOrgLimitOverrides :one
SELECT * FROM org_limit_overrides WHERE org_id = $1;

-- name: UpsertOrgLimitOverrides :one
INSERT INTO org_limit_overrides (
    org_id,
    workspaces_per_org,
    members_per_org,
    invitations_per_org,
    environments_per_workspace,
    resources_per_workspace,
    webhooks_per_workspace,
    tokens_per_entity,
    domains_per_resource,
    secrets_per_resource,
    deployment_history,
    build_history,
    reason,
    updated_by
)
VALUES (
    sqlc.arg('org_id'),
    sqlc.narg('workspaces_per_org'),
    sqlc.narg('members_per_org'),
    sqlc.narg('invitations_per_org'),
    sqlc.narg('environments_per_workspace'),
    sqlc.narg('resources_per_workspace'),
    sqlc.narg('webhooks_per_workspace'),
    sqlc.narg('tokens_per_entity'),
    sqlc.narg('domains_per_resource'),
    sqlc.narg('secrets_per_resource'),
    sqlc.narg('deployment_history'),
    sqlc.narg('build_history'),
    sqlc.arg('reason'),
    sqlc.narg('updated_by')
)
ON CONFLICT (org_id) DO UPDATE SET
    workspaces_per_org = EXCLUDED.workspaces_per_org,
    members_per_org = EXCLUDED.members_per_org,
    invitations_per_org = EXCLUDED.invitations_per_org,
    environments_per_workspace = EXCLUDED.environments_per_workspace,
    resources_per_workspace = EXCLUDED.resources_per_workspace,
    webhooks_per_workspace = EXCLUDED.webhooks_per_workspace,
    tokens_per_entity = EXCLUDED.tokens_per_entity,
    domains_per_resource = EXCLUDED.domains_per_resource,
    secrets_per_resource = EXCLUDED.secrets_per_resource,
    deployment_history = EXCLUDED.deployment_history,
    build_history = EXCLUDED.build_history,
    reason = EXCLUDED.reason,
    updated_by = EXCLUDED.updated_by,
    updated_at = NOW()
RETURNING *;

-- name: LockOrgForCreate :one
SELECT id FROM organizations WHERE id = $1 FOR NO KEY UPDATE;

-- name: LockWorkspaceForCreate :one
SELECT org_id FROM workspaces WHERE id = $1 FOR NO KEY UPDATE;

-- name: LockTokenOwner :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(entity_id)::uuid::text, 0));

-- name: CountOrgsCreatedBy :one
SELECT COUNT(*) FROM organizations WHERE created_by = $1;

-- name: CountOrgWorkspaces :one
SELECT COUNT(*) FROM workspaces WHERE org_id = $1;

-- name: CountOrgMembers :one
SELECT COUNT(DISTINCT user_id) FROM user_scopes WHERE entity_type = 'organization' AND entity_id = $1;

-- name: CountOpenOrgInvitations :one
SELECT COUNT(*) FROM invitations
WHERE org_id = $1 AND claimed_at IS NULL AND revoked_at IS NULL AND expires_at > NOW();

-- name: CountWorkspaceEnvironments :one
SELECT COUNT(*) FROM environments WHERE workspace_id = $1;

-- name: CountWorkspaceResources :one
SELECT COUNT(*) FROM resources WHERE workspace_id = $1;

-- name: CountEntityAPITokens :one
SELECT COUNT(*) FROM api_tokens WHERE entity_type = $1 AND entity_id = $2;
