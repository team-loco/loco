-- name: CloseSystemBootstrap :execrows
INSERT INTO system_bootstrap (bootstrapped_by) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: SystemAdminExists :one
SELECT EXISTS (
    SELECT 1 FROM user_scopes WHERE entity_type = 'system' AND scope = 'admin'
);
