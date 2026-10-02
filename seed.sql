-- Seed data for local development
-- Agent token (plaintext): loco-dev-agent-token
-- Agent token SHA256:      118e49a9b48a163fe352bc6c443569a1fc65e5889fec798058771b36d7a12579
--
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
    '118e49a9b48a163fe352bc6c443569a1fc65e5889fec798058771b36d7a12579'
) ON CONFLICT DO NOTHING;

INSERT INTO platform_domains (domain, is_active)
VALUES ('onloco.app', true)
ON CONFLICT DO NOTHING;
