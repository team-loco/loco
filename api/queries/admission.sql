-- name: ConsumeAdmission :one
INSERT INTO api_admission (key, used, expires_at)
VALUES (sqlc.arg(key), 1, statement_timestamp() + sqlc.arg(window_seconds)::double precision * interval '1 second')
ON CONFLICT (key) DO UPDATE SET
    used = CASE WHEN api_admission.expires_at <= statement_timestamp() THEN 1 ELSE api_admission.used + 1 END,
    expires_at = CASE WHEN api_admission.expires_at <= statement_timestamp() THEN EXCLUDED.expires_at ELSE api_admission.expires_at END
RETURNING used, GREATEST(1, ceil(extract(epoch FROM (expires_at - statement_timestamp()))))::bigint AS retry_seconds;

-- name: GetAdmissionTarget :one
WITH target AS (
 SELECT workspace_id FROM resources WHERE sqlc.arg(kind)::text = 'resource_id' AND id = sqlc.arg(id)::uuid
 UNION ALL
 SELECT id FROM workspaces WHERE sqlc.arg(kind)::text = 'workspace_id' AND id = sqlc.arg(id)::uuid
 UNION ALL
 SELECT workspace_id FROM environments WHERE sqlc.arg(kind)::text = 'environment_id' AND id = sqlc.arg(id)::uuid
 UNION ALL
 SELECT r.workspace_id FROM builds b JOIN resources r ON r.id = b.resource_id WHERE sqlc.arg(kind)::text = 'build_id' AND b.id = sqlc.arg(id)::uuid
 UNION ALL
 SELECT r.workspace_id FROM deployments d JOIN resources r ON r.id = d.resource_id WHERE sqlc.arg(kind)::text = 'deployment_id' AND d.id = sqlc.arg(id)::uuid
 UNION ALL
 SELECT r.workspace_id FROM resource_domains d JOIN resources r ON r.id = d.resource_id WHERE sqlc.arg(kind)::text = 'domain_id' AND d.id = sqlc.arg(id)::uuid
)
SELECT target.workspace_id, w.org_id, EXISTS (
 SELECT 1 FROM resources r WHERE r.workspace_id = w.id AND r.id = ANY(sqlc.arg(resource_ids)::uuid[])
) AS resource_grant FROM target JOIN workspaces w ON w.id = target.workspace_id;
