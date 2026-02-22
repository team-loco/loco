-- Agent support: add columns to clusters table for agent registration and health

-- Agent token hash for authentication
ALTER TABLE clusters ADD COLUMN agent_token_hash TEXT;

-- Last heartbeat timestamp
ALTER TABLE clusters ADD COLUMN last_heartbeat TIMESTAMPTZ;

-- Cluster capacity reported by agent
ALTER TABLE clusters ADD COLUMN capacity_cpu_millicores BIGINT;
ALTER TABLE clusters ADD COLUMN capacity_memory_bytes BIGINT;

-- Agent version
ALTER TABLE clusters ADD COLUMN agent_version TEXT;

-- Index for token lookup during auth
CREATE UNIQUE INDEX idx_clusters_agent_token_hash
    ON clusters(agent_token_hash)
    WHERE agent_token_hash IS NOT NULL;
