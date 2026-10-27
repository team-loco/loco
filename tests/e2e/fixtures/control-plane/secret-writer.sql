INSERT INTO resources (id, workspace_id, name, type, description, spec, spec_version)
VALUES (:'resource', '00000000-0000-7000-8000-000000000003', 'e2e-secrets', 'service', '', '{}', 1)
ON CONFLICT DO NOTHING;

INSERT INTO user_scopes (user_id, scope, entity_type, entity_id)
VALUES ('00000000-0000-7000-8000-000000000001', 'write', 'resource', :'resource')
ON CONFLICT DO NOTHING;

INSERT INTO session_tokens (access_token_hash, refresh_token_hash, user_id, access_expires_at, refresh_expires_at)
VALUES (
    encode(sha256(convert_to(:'token', 'UTF8')), 'hex'),
    encode(sha256(convert_to('refresh-' || :'token', 'UTF8')), 'hex'),
    '00000000-0000-7000-8000-000000000001',
    NOW() + INTERVAL '1 day',
    NOW() + INTERVAL '1 day'
)
ON CONFLICT DO NOTHING;
