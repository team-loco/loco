-- name: CloseSystemBootstrap :execrows
INSERT INTO system_bootstrap (bootstrapped_by) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: SetSystemBootstrapOwner :exec
UPDATE system_bootstrap SET bootstrapped_by = $1 WHERE singleton;

-- name: SystemAdminExists :one
SELECT EXISTS (
    SELECT 1 FROM user_scopes WHERE entity_type = 'system' AND scope = 'admin'
);
