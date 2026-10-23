-- +goose Up
-- Deployment status enum
CREATE TYPE deployment_status AS ENUM (
    'pending',
    'deploying',
    'running',
    'succeeded',
    'failed',
    'canceled'
);

-- Resource type enum
CREATE TYPE resource_type AS ENUM (
    'service',
    'worker',
    'database',
    'cache',
    'queue',
    'blob'
);

CREATE TYPE build_status AS ENUM (
    'awaiting_upload',
    'queued',
    'running',
    'succeeded',
    'failed',
    'canceled'
);

-- Domain source enum (who manages the domain)
CREATE TYPE domain_source AS ENUM ('platform_provided', 'user_provided');

-- Environments: workspace-level isolation concept
CREATE TABLE
    environments (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        description TEXT,
        environment_type TEXT NOT NULL DEFAULT 'production' CHECK (
            environment_type IN ('staging', 'production')
        ),
        created_by UUID NOT NULL REFERENCES users (id),
        revision BIGINT NOT NULL DEFAULT 0,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (workspace_id, name)
    );

CREATE INDEX IF NOT EXISTS idx_environments_workspace_id_created_at ON environments (workspace_id, created_at);

CREATE INDEX idx_environments_created_by ON environments (created_by);

-- One data-encryption key per environment, wrapped by the configured key provider
CREATE TABLE
    environment_keys (
        environment_id UUID PRIMARY KEY REFERENCES environments (id) ON DELETE CASCADE,
        provider TEXT NOT NULL,
        kek_id TEXT NOT NULL,
        wrapped_dek BYTEA NOT NULL,
        format_version SMALLINT NOT NULL DEFAULT 1,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        rewrapped_at TIMESTAMPTZ
    );

-- Clusters table
CREATE TABLE
    clusters (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        name TEXT NOT NULL,
        cluster_key UUID UNIQUE,
        removed_at TIMESTAMPTZ,
        region TEXT NOT NULL,
        provider TEXT NOT NULL,
        is_active BOOLEAN NOT NULL,
        is_default BOOLEAN NOT NULL,
        endpoint TEXT,
        health_status TEXT CHECK (
            health_status IN ('healthy', 'unhealthy', 'degraded')
        ),
        last_health_check TIMESTAMPTZ,
        last_heartbeat TIMESTAMPTZ,
        capacity_cpu_millicores BIGINT,
        capacity_memory_bytes BIGINT,
        agent_version TEXT,
        -- publicly resolvable FQDN of this cluster's gateway; a hostname, never an address
        gateway_hostname TEXT,
        sync_generation BIGINT NOT NULL DEFAULT 0,
        domains TEXT[] NOT NULL DEFAULT '{}',
        agent_public_key BYTEA CHECK (octet_length(agent_public_key) = 32),
        join_token_hash TEXT UNIQUE,
        join_token_expires_at TIMESTAMPTZ,
        join_token_used_at TIMESTAMPTZ,
        builds_enabled BOOLEAN NOT NULL DEFAULT false,
        tier TEXT NOT NULL DEFAULT 'production' CHECK (tier IN ('staging', 'production')),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX idx_clusters_region ON clusters (region);

CREATE INDEX idx_clusters_is_active ON clusters (is_active);

CREATE INDEX idx_clusters_tier ON clusters (tier);

CREATE TABLE
    cluster_assertions (
        cluster_id UUID NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
        jti TEXT NOT NULL,
        expires_at TIMESTAMPTZ NOT NULL,
        PRIMARY KEY (cluster_id, jti)
    );

CREATE INDEX idx_cluster_assertions_expires_at ON cluster_assertions (expires_at);

-- Resources table
CREATE TABLE
    resources (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        type resource_type NOT NULL,
        description TEXT NOT NULL,
        spec JSONB NOT NULL,
        spec_version INT NOT NULL,
        partial TEXT CHECK (partial ~ '^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$'),
        max_apply_attempts INT CHECK (max_apply_attempts > 0),
        apply_retry_delay_seconds INT CHECK (apply_retry_delay_seconds > 0),
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (workspace_id, name)
    );

CREATE INDEX IF NOT EXISTS idx_resources_workspace_created_id_desc ON resources (workspace_id, created_at DESC, id DESC);

-- Secret values of one service in one environment, AES-256-GCM ciphertext under the environment's data-encryption key
CREATE TABLE
    secrets (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
        environment_id UUID NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
        name TEXT NOT NULL CHECK (name ~ '^[A-Z_][A-Z0-9_]*$'),
        version INT NOT NULL DEFAULT 1 CHECK (version > 0),
        nonce BYTEA NOT NULL,
        ciphertext BYTEA NOT NULL,
        format_version SMALLINT NOT NULL DEFAULT 1,
        updated_by_type TEXT NOT NULL,
        updated_by_id UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (resource_id, environment_id, name)
    );

CREATE INDEX idx_secrets_environment_id ON secrets (environment_id);

CREATE TABLE
    resource_domains (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
        environment_id UUID NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
        domain TEXT NOT NULL UNIQUE,
        domain_source domain_source NOT NULL,
        subdomain_label TEXT,
        platform_domain TEXT,
        is_primary BOOLEAN NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        CONSTRAINT domain_source_check CHECK (
            (
                domain_source = 'platform_provided'
                AND subdomain_label IS NOT NULL
                AND platform_domain IS NOT NULL
            )
            OR (
                domain_source = 'user_provided'
                AND subdomain_label IS NULL
                AND platform_domain IS NULL
            )
        )
    );

CREATE INDEX IF NOT EXISTS idx_resource_domains_resource_id_primary_created ON resource_domains (resource_id, is_primary DESC, created_at ASC);

CREATE INDEX idx_resource_domains_environment_id ON resource_domains (environment_id);

-- Enforce max 1 primary domain per resource in each environment
CREATE UNIQUE INDEX uniq_resource_primary_domain ON resource_domains (resource_id, environment_id)
WHERE
    is_primary = true;

-- Ensure platform subdomain uniqueness
CREATE UNIQUE INDEX uniq_platform_subdomain ON resource_domains (platform_domain, subdomain_label)
WHERE
    domain_source = 'platform_provided';

-- Deployments table (immutable, single-region)
CREATE TABLE
    deployments (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
        cluster_id UUID NOT NULL REFERENCES clusters (id) ON DELETE RESTRICT,
        region TEXT NOT NULL,
        replicas INT NOT NULL,
        status deployment_status NOT NULL,
        is_active BOOLEAN NOT NULL,
        message TEXT NOT NULL,
        environment_id UUID NOT NULL REFERENCES environments (id),
        spec JSONB NOT NULL,
        spec_version INT NOT NULL,
        secret_names TEXT[] NOT NULL DEFAULT '{}',
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        started_at TIMESTAMPTZ NOT NULL,
        completed_at TIMESTAMPTZ,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX idx_deployments_cluster_id ON deployments (cluster_id);

CREATE INDEX idx_deployments_region ON deployments (region);

CREATE UNIQUE INDEX uniq_deployments_resource_region_active ON deployments (resource_id, environment_id, region)
WHERE
    is_active = true;

CREATE INDEX idx_deployments_status_created_at ON deployments (status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_deployments_resource_created_id_desc ON deployments (resource_id, created_at DESC, id DESC);

CREATE INDEX idx_deployments_environment_id ON deployments (environment_id);

CREATE TABLE
    placements (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL,
        cluster_id UUID NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
        region TEXT NOT NULL,
        deployment_id UUID REFERENCES deployments (id) ON DELETE SET NULL,
        environment_id UUID NOT NULL,
        secret_names TEXT[] NOT NULL DEFAULT '{}',
        desired_revision BIGINT NOT NULL DEFAULT 1 CHECK (desired_revision > 0),
        desired_spec JSONB,
        desired_deleted BOOLEAN NOT NULL DEFAULT false,
        applied_revision BIGINT NOT NULL DEFAULT 0,
        applied_at TIMESTAMPTZ,
        applied_error TEXT,
        retry_after TIMESTAMPTZ,
        apply_attempts INT NOT NULL DEFAULT 0,
        apply_failed BOOLEAN NOT NULL DEFAULT false,
        observed_revision BIGINT NOT NULL DEFAULT 0,
        ready BOOLEAN NOT NULL DEFAULT false,
        ready_replicas INT NOT NULL DEFAULT 0,
        status_phase TEXT NOT NULL DEFAULT '',
        status_message TEXT NOT NULL DEFAULT '',
        status_updated_at TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (resource_id, cluster_id),
        CHECK (desired_deleted OR desired_spec IS NOT NULL)
    );

CREATE INDEX idx_placements_pending ON placements (cluster_id)
WHERE
    applied_revision < desired_revision;

CREATE INDEX idx_placements_cluster_id ON placements (cluster_id);

CREATE INDEX idx_placements_deployment_id ON placements (deployment_id);

CREATE TABLE
    placement_windows (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL,
        cluster_id UUID NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
        workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
        placed_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        removed_at TIMESTAMPTZ,
        CHECK (removed_at IS NULL OR removed_at >= placed_at)
    );

CREATE UNIQUE INDEX uniq_placement_windows_open ON placement_windows (resource_id, cluster_id)
WHERE
    removed_at IS NULL;

CREATE INDEX idx_placement_windows_cluster_id ON placement_windows (cluster_id);

CREATE TABLE
    builds (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
        cluster_id UUID REFERENCES clusters (id) ON DELETE SET NULL,
        status build_status NOT NULL,
        source_type TEXT NOT NULL CHECK (source_type IN ('upload')),
        source_key TEXT NOT NULL UNIQUE,
        source_size BIGINT NOT NULL,
        dockerfile_path TEXT NOT NULL,
        context TEXT NOT NULL DEFAULT '.',
        image_repository TEXT NOT NULL,
        image_digest TEXT,
        cache_digest TEXT,
        message TEXT NOT NULL DEFAULT '',
        created_by UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        started_at TIMESTAMPTZ,
        finished_at TIMESTAMPTZ,
        source_deleted_at TIMESTAMPTZ,
        image_deleted_at TIMESTAMPTZ,
        CHECK (
            status <> 'succeeded'
            OR image_digest IS NOT NULL
        )
    );

CREATE INDEX idx_builds_resource_created_at ON builds (resource_id, created_at DESC);

CREATE INDEX idx_builds_cluster_active ON builds (cluster_id)
WHERE
    status IN ('queued', 'running');

CREATE INDEX idx_builds_awaiting_upload ON builds (created_at)
WHERE
    status = 'awaiting_upload';

CREATE INDEX idx_builds_source_undeleted ON builds (finished_at, id)
WHERE
    source_deleted_at IS NULL
    AND status IN ('succeeded', 'failed', 'canceled');

CREATE INDEX idx_builds_succeeded_resource ON builds (resource_id, finished_at DESC, id DESC)
WHERE
    status = 'succeeded';

-- +goose Down
DROP TABLE IF EXISTS builds;
DROP TABLE IF EXISTS placement_windows;
DROP TABLE IF EXISTS placements;
DROP TABLE IF EXISTS deployments;
DROP TABLE IF EXISTS resource_domains;
DROP TABLE IF EXISTS secrets;
DROP TABLE IF EXISTS resources;
DROP TABLE IF EXISTS cluster_assertions;
DROP TABLE IF EXISTS clusters;
DROP TABLE IF EXISTS environment_keys;
DROP TABLE IF EXISTS environments;
DROP TYPE IF EXISTS domain_source;
DROP TYPE IF EXISTS build_status;
DROP TYPE IF EXISTS resource_type;
DROP TYPE IF EXISTS deployment_status;
