-- Test user
INSERT INTO users (id, external_id, email, name)
VALUES (
    '00000000-0000-7000-8000-000000000001',
    'github:e2e-test-user',
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

-- Test cluster with agent token
INSERT INTO clusters (id, name, region, provider, is_active, is_default, agent_token_hash, tier)
VALUES (
    '00000000-0000-7000-8000-000000000005',
    'e2e-test-cluster',
    'us-east-1',
    'kind',
    true,
    true,
    encode(sha256(convert_to(:'agent_token', 'UTF8')), 'hex'),
    'production'
) ON CONFLICT DO NOTHING;

-- Platform domain for test resources
INSERT INTO platform_domains (id, domain, is_active)
VALUES ('00000000-0000-7000-8000-000000000006', 'e2e.test.local', true)
ON CONFLICT DO NOTHING;

INSERT INTO infra_stacks (id, environment_id, name)
VALUES ('00000000-0000-7000-8000-000000000007', '00000000-0000-7000-8000-000000000004', 'e2e')
ON CONFLICT DO NOTHING;
