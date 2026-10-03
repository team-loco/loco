-- tier must match the environment_type a deployment targets. CreateWorkspace
-- always creates a "production" environment (environment_type 'production'), and
-- GetActiveClusterForRegion filters on `tier = $2 AND health_status = 'healthy'`,
-- so a cluster seeded as 'dev' with a null health_status can never serve the
-- default environment and every deploy fails with "no active cluster available".
-- health_status is normally written by the agent heartbeat; it is seeded here so
-- a fresh local environment can deploy before the first heartbeat lands.

INSERT INTO clusters (name, region, provider, is_active, is_default, tier, health_status, agent_token_hash)
VALUES (
    'loco-local',
    'us-east-1',
    'local',
    true,
    true,
    'production',
    'healthy',
    encode(sha256(convert_to(:'agent_token', 'UTF8')), 'hex')
) ON CONFLICT (name) DO UPDATE SET agent_token_hash = EXCLUDED.agent_token_hash;

INSERT INTO platform_domains (domain, is_active)
VALUES ('onloco.app', true), ('onloco.build', true)
ON CONFLICT DO NOTHING;
