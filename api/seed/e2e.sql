-- Test user
INSERT INTO users (id, email, name)
VALUES (
    '00000000-0000-7000-8000-000000000001',
    'e2e@test.local',
    'E2E Test User'
) ON CONFLICT DO NOTHING;

-- Test organization
INSERT INTO organizations (id, name, created_by)
VALUES (
    '00000000-0000-7000-8000-000000000002',
    'e2e-test-org',
    '00000000-0000-7000-8000-000000000001'
) ON CONFLICT DO NOTHING;

INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
VALUES
    ('00000000-0000-7000-8000-000000000001', 'read', 'user', '00000000-0000-7000-8000-000000000001'),
    ('00000000-0000-7000-8000-000000000001', 'write', 'user', '00000000-0000-7000-8000-000000000001'),
    ('00000000-0000-7000-8000-000000000001', 'admin', 'user', '00000000-0000-7000-8000-000000000001'),
    ('00000000-0000-7000-8000-000000000001', 'member', 'organization', '00000000-0000-7000-8000-000000000002')
ON CONFLICT DO NOTHING;

-- Test workspace
INSERT INTO workspaces (id, org_id, name, description, created_by)
VALUES (
    '00000000-0000-7000-8000-000000000003',
    '00000000-0000-7000-8000-000000000002',
    'e2e-test-workspace',
    'Workspace for e2e tests',
    '00000000-0000-7000-8000-000000000001'
) ON CONFLICT DO NOTHING;

-- Test environment for deployments
INSERT INTO environments (id, workspace_id, name, environment_type, created_by)
VALUES (
    '00000000-0000-7000-8000-000000000004',
    '00000000-0000-7000-8000-000000000003',
    'production',
    'production',
    '00000000-0000-7000-8000-000000000001'
) ON CONFLICT DO NOTHING;
