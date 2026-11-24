-- +goose Up
-- Deployment status enum
CREATE TYPE deployment_status AS ENUM (
    'building',
    'deploying',
    'running',
    'degraded',
    'crashed',
    'failed',
    'canceled',
    'superseded'
);

-- One region of a deployment; pending waits for the deployment's build.
CREATE TYPE placement_status AS ENUM (
    'pending',
    'deploying',
    'running',
    'degraded',
    'crashed',
    'failed',
    'canceled',
    'superseded'
);

CREATE TYPE deployment_trigger AS ENUM ('deploy', 'redeploy', 'rollback', 'scale');

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
CREATE TYPE domain_source AS ENUM ('app', 'custom');

CREATE TYPE cluster_role AS ENUM ('apps', 'builds', 'all');

-- Environments: workspace-level isolation concept
CREATE TABLE
    environments (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
        slug TEXT NOT NULL CHECK (slug ~ '^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$'),
        name TEXT NOT NULL CHECK (char_length(name) <= 63 AND name ~ '\S'),
        description TEXT,
        created_by UUID NOT NULL,
        revision BIGINT NOT NULL DEFAULT 0,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (workspace_id, slug)
    );

CREATE UNIQUE INDEX uniq_environments_workspace_lower_name ON environments (workspace_id, lower(name));

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
        platforms TEXT[] NOT NULL DEFAULT '{}',
        capacity_cpu_millicores BIGINT,
        capacity_memory_bytes BIGINT,
        agent_version TEXT,
        sync_generation BIGINT NOT NULL DEFAULT 0,
        domains TEXT[] NOT NULL DEFAULT '{}',
        agent_public_key BYTEA CHECK (octet_length(agent_public_key) = 32),
        join_token_hash TEXT UNIQUE,
        join_token_expires_at TIMESTAMPTZ,
        join_token_used_at TIMESTAMPTZ,
        -- reported by the agent from its install; 'all' until the agent first registers
        role cluster_role NOT NULL DEFAULT 'all',
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW ()
    );

CREATE INDEX idx_clusters_region ON clusters (region);

CREATE INDEX idx_clusters_is_active ON clusters (is_active);


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
        slug TEXT NOT NULL CHECK (slug ~ '^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$'),
        name TEXT NOT NULL CHECK (char_length(name) <= 63 AND name ~ '\S'),
        type resource_type NOT NULL,
        description TEXT NOT NULL,
        spec JSONB NOT NULL,
        spec_version INT NOT NULL,
        revision BIGINT NOT NULL DEFAULT 1,
        partial TEXT CHECK (partial ~ '^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$'),
        restart_policy TEXT,
        max_restarts INT,
        deploy_timeout_seconds INT,
        created_by UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (workspace_id, slug)
    );

CREATE UNIQUE INDEX uniq_resources_workspace_lower_name ON resources (workspace_id, lower(name));

CREATE INDEX IF NOT EXISTS idx_resources_workspace_created_id_desc ON resources (workspace_id, created_at DESC, id DESC);

-- Secret values of one environment, AES-256-GCM ciphertext under the environment's data-encryption key
CREATE TABLE
    secrets (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        environment_id UUID NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
        name TEXT NOT NULL CHECK (name ~ '^[A-Z_][A-Z0-9_]*$'),
        version INT NOT NULL DEFAULT 1 CHECK (version > 0),
        nonce BYTEA NOT NULL,
        ciphertext BYTEA NOT NULL,
        format_version SMALLINT NOT NULL DEFAULT 1,
        updated_by_type TEXT NOT NULL,
        updated_by_id UUID NOT NULL,
        created_by_type TEXT NOT NULL,
        created_by_id UUID NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (environment_id, name)
    );

CREATE TABLE
    resource_domains (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
        environment_id UUID NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
        domain TEXT NOT NULL UNIQUE,
        domain_source domain_source NOT NULL,
        subdomain_label TEXT,
        app_domain TEXT,
        is_primary BOOLEAN NOT NULL,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        CONSTRAINT domain_source_check CHECK (
            (
                domain_source = 'app'
                AND subdomain_label IS NOT NULL
                AND app_domain IS NOT NULL
            )
            OR (
                domain_source = 'custom'
                AND subdomain_label IS NULL
                AND app_domain IS NULL
            )
        )
    );

CREATE INDEX IF NOT EXISTS idx_resource_domains_resource_id_primary_created ON resource_domains (resource_id, is_primary DESC, created_at ASC);

CREATE INDEX idx_resource_domains_environment_id ON resource_domains (environment_id);

-- Enforce max 1 primary domain per resource in each environment
CREATE UNIQUE INDEX uniq_resource_primary_domain ON resource_domains (resource_id, environment_id)
WHERE
    is_primary = true;

-- Ensure app subdomain uniqueness
CREATE UNIQUE INDEX uniq_app_subdomain ON resource_domains (app_domain, subdomain_label)
WHERE
    domain_source = 'app';

CREATE TYPE deployment_actor_type AS ENUM ('user', 'api_token');

CREATE TYPE deployment_method AS ENUM ('api_token', 'cli', 'web');

-- One rollout request for a resource in an environment; its placements fan it out per region.
CREATE TABLE
    deployments (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
        status deployment_status NOT NULL,
        environment_id UUID NOT NULL REFERENCES environments (id),
        spec JSONB NOT NULL,
        spec_version INT NOT NULL,
        trigger deployment_trigger NOT NULL,
        source_deployment_id UUID REFERENCES deployments (id) ON DELETE SET NULL,
        actor_type deployment_actor_type NOT NULL,
        actor_id UUID NOT NULL,
        method deployment_method NOT NULL,
        canceled_by_type deployment_actor_type,
        canceled_by_id UUID,
        commit_sha TEXT,
        commit_dirty BOOLEAN NOT NULL DEFAULT false,
        -- the variables with references to other resources resolved, as plain values and as
        -- templates that still reference secrets
        plain_env JSONB NOT NULL DEFAULT '{}',
        secret_env JSONB NOT NULL DEFAULT '{}',
        activity_seq BIGINT NOT NULL DEFAULT 0,
        -- the W3C trace context of the request that created the deployment
        traceparent TEXT,
        -- serving, with every serving placement's current revision deployed
        ready BOOLEAN NOT NULL DEFAULT false,
        -- insert time, which orders the deployments of a resource
        created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp (),
        -- when the deployment first became ready
        started_at TIMESTAMPTZ,
        completed_at TIMESTAMPTZ,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        CHECK ((canceled_by_type IS NULL) = (canceled_by_id IS NULL))
    );

CREATE INDEX idx_deployments_status_created_at ON deployments (status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_deployments_resource_created_id_desc ON deployments (resource_id, created_at DESC, id DESC);

CREATE INDEX idx_deployments_environment_created_id_desc ON deployments (environment_id, created_at DESC, id DESC);

CREATE INDEX idx_deployments_resource_environment_created_id_desc ON deployments (resource_id, environment_id, created_at DESC, id DESC);

CREATE INDEX idx_deployments_source_deployment_id ON deployments (source_deployment_id);

-- One region of a deployment.
CREATE TABLE
    deployment_placements (
        deployment_id UUID NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
        cluster_id UUID NOT NULL REFERENCES clusters (id) ON DELETE RESTRICT,
        region TEXT NOT NULL,
        replicas INT NOT NULL,
        status placement_status NOT NULL,
        -- '' while the build runs
        image TEXT NOT NULL,
        superseded_by UUID REFERENCES deployments (id) ON DELETE SET NULL,
        -- the current revision became healthy with every replica available
        deployed BOOLEAN NOT NULL DEFAULT false,
        -- when the placement was first deployed
        started_at TIMESTAMPTZ,
        completed_at TIMESTAMPTZ,
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        PRIMARY KEY (deployment_id, cluster_id)
    );

CREATE INDEX idx_deployment_placements_cluster_id ON deployment_placements (cluster_id);

CREATE INDEX idx_deployment_placements_superseded_by ON deployment_placements (superseded_by);

-- +goose StatementBegin
-- The aggregate status of a deployment: canceled when a user canceled it or every placement
-- was canceled, building while every placement waits for the build, then the worst placement.
CREATE FUNCTION deployment_status_from_placements (deployment UUID) RETURNS deployment_status LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN d.canceled_by_id IS NOT NULL OR NOT EXISTS (
            SELECT 1 FROM deployment_placements dp
            WHERE dp.deployment_id = d.id AND dp.status <> 'canceled') THEN 'canceled'
        WHEN bool_and(dp.status = 'pending') THEN 'building'
        WHEN bool_or(dp.status = 'failed') THEN 'failed'
        WHEN bool_or(dp.status = 'crashed') THEN 'crashed'
        WHEN bool_or(dp.status IN ('pending', 'deploying')) THEN 'deploying'
        WHEN bool_or(dp.status = 'degraded') THEN 'degraded'
        WHEN bool_or(dp.status = 'running') THEN 'running'
        ELSE 'superseded'
    END::deployment_status
    FROM deployments d
    LEFT JOIN deployment_placements dp ON dp.deployment_id = d.id AND dp.status <> 'canceled'
    WHERE d.id = deployment
    GROUP BY d.id
$$;
-- +goose StatementEnd

CREATE TABLE deployment_rollouts (
    deployment_id UUID NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    cluster_id UUID NOT NULL,
    revision BIGINT NOT NULL,
    snapshot JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (deployment_id, cluster_id, revision),
    FOREIGN KEY (deployment_id, cluster_id) REFERENCES deployment_placements (deployment_id, cluster_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX uniq_rollout_placement_revision
ON deployment_rollouts ((snapshot->>'placementId'), revision);

CREATE TABLE deployment_activity (
    deployment_id UUID NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    seq BIGINT NOT NULL,
    revision BIGINT NOT NULL,
    at TIMESTAMPTZ NOT NULL,
    severity TEXT NOT NULL,
    replica_uid TEXT NOT NULL DEFAULT '',
    replica_name TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    -- NULL for an entry about the whole deployment, such as its build
    cluster_id UUID,
    region TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (deployment_id, seq)
);

CREATE TABLE
    placements (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL,
        cluster_id UUID NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
        region TEXT NOT NULL,
        deployment_id UUID REFERENCES deployments (id) ON DELETE SET NULL,
        environment_id UUID NOT NULL,
        -- variables whose templates reference secrets, rendered when the placement is sent
        secret_env JSONB NOT NULL DEFAULT '{}',
        desired_revision BIGINT NOT NULL DEFAULT 1 CHECK (desired_revision > 0),
        desired_spec JSONB,
        desired_deleted BOOLEAN NOT NULL DEFAULT false,
        -- the last restart, sent with every revision so deploys and cancels keep the restarted replicas
        restarted_at TIMESTAMPTZ,
        applied_revision BIGINT NOT NULL DEFAULT 0,
        applied_error TEXT,
        retry_after TIMESTAMPTZ,
        apply_attempts INT NOT NULL DEFAULT 0,
        apply_failed BOOLEAN NOT NULL DEFAULT false,
        observed_revision BIGINT NOT NULL DEFAULT 0,
        ready_replicas INT NOT NULL DEFAULT 0,
        problem_reason TEXT NOT NULL DEFAULT '',
        status_updated_at TIMESTAMPTZ,
        region_status TEXT NOT NULL DEFAULT '',
        region_status_since TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        UNIQUE (resource_id, environment_id, cluster_id),
        CHECK (desired_deleted OR desired_spec IS NOT NULL)
    );

-- +goose StatementBegin
CREATE FUNCTION resource_suspended (resource UUID) RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM resources r
        JOIN workspaces w ON w.id = r.workspace_id
        JOIN organizations o ON o.id = w.org_id
        WHERE r.id = resource AND o.suspended_at IS NOT NULL
    )
$$;
-- +goose StatementEnd

-- Retain exact observation context through pending revisions and deletion ACKs.
-- Only the read projection invalidates readiness while paused or awaiting a resumed report.
CREATE VIEW observed_placement_rollouts AS
SELECT p.id AS placement_id, p.resource_id, p.environment_id, p.cluster_id, p.region,
       reported.id AS reported_deployment_id, ro.snapshot,
       CASE WHEN NOT resource_suspended(p.resource_id)
          AND (p.observed_revision = p.desired_revision OR p.ready_replicas > 0)
         THEN COALESCE(ro.snapshot->'replicas', '[]'::jsonb)
         ELSE '[]'::jsonb END AS replicas
FROM placements p
JOIN deployments reported ON reported.resource_id = p.resource_id
    AND reported.environment_id = p.environment_id
JOIN deployment_rollouts ro ON ro.deployment_id = reported.id AND ro.cluster_id = p.cluster_id
    AND ro.revision = p.observed_revision
    AND ro.snapshot->>'placementId' = p.id::text;

-- The deployment each region of a resource's environment runs or is about to run: the
-- placement's deployment, or a newer one still waiting for its build.
CREATE VIEW live_deployment_placements AS
SELECT DISTINCT ON (d.resource_id, d.environment_id, dp.cluster_id)
       d.id AS deployment_id, d.resource_id, d.environment_id, dp.cluster_id, dp.region, dp.replicas, dp.status
FROM deployment_placements dp
JOIN deployments d ON d.id = dp.deployment_id
WHERE dp.status = 'pending' OR EXISTS (
    SELECT 1 FROM placements p
    WHERE p.deployment_id = dp.deployment_id AND p.cluster_id = dp.cluster_id AND NOT p.desired_deleted
)
ORDER BY d.resource_id, d.environment_id, dp.cluster_id, d.created_at DESC, d.id DESC;

-- What a region's status is derived from: its live deployment, placement, latest report and cluster.
CREATE VIEW resource_region_inputs AS
SELECT l.resource_id,
       l.environment_id,
       e.slug AS environment_slug,
       l.region,
       l.cluster_id,
       l.deployment_id,
       l.status AS placement_status,
       l.replicas AS desired_replicas,
       COALESCE((SELECT ro.snapshot->'problem' FROM deployment_rollouts ro
                 WHERE ro.deployment_id = l.deployment_id AND ro.cluster_id = l.cluster_id
                 ORDER BY revision DESC LIMIT 1), 'null'::jsonb)::jsonb AS problem,
       (p.id IS NOT NULL)::boolean AS placed,
       p.deployment_id AS placed_deployment_id,
       COALESCE(p.ready_replicas, 0)::int AS ready_replicas,
       p.status_updated_at,
       COALESCE(p.region_status, '')::text AS stored_status,
       p.region_status_since,
       COALESCE((SELECT ro.replicas FROM observed_placement_rollouts ro WHERE ro.placement_id = p.id),
                '[]'::jsonb)::jsonb AS replicas,
       c.last_heartbeat,
       c.health_status,
       ARRAY(
           SELECT rd.domain FROM resource_domains rd
           WHERE rd.resource_id = l.resource_id
             AND rd.environment_id = l.environment_id
             AND rd.domain_source = 'app'
             AND NOT (rd.app_domain = ANY(c.domains))
           ORDER BY rd.domain
       )::text[] AS unserved_domains
FROM live_deployment_placements l
JOIN environments e ON e.id = l.environment_id
JOIN clusters c ON c.id = l.cluster_id
LEFT JOIN placements p ON p.resource_id = l.resource_id AND p.environment_id = l.environment_id
    AND p.cluster_id = l.cluster_id AND NOT p.desired_deleted;

-- +goose StatementBegin
-- What overrides a region's replica-based status: a cluster cause, unserved app domains, or '' for none.
CREATE FUNCTION region_status_cause (
    last_heartbeat TIMESTAMPTZ, health_status TEXT, silent_after INTERVAL,
    ready INT, desired INT, placement placement_status, unserved_domains INT
) RETURNS TEXT LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN last_heartbeat IS NULL THEN 'cluster_never_reported'
        WHEN now() - last_heartbeat > silent_after THEN 'cluster_silent'
        WHEN health_status IS DISTINCT FROM 'healthy' THEN 'cluster_unhealthy'
        WHEN ready >= desired AND placement <> 'degraded' AND unserved_domains > 0 THEN 'domains_unserved'
        ELSE ''
    END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A region's status as the ResourceStatus enum name, from its cause, replicas and placement.
CREATE FUNCTION region_status (cause TEXT, ready INT, desired INT, placement placement_status)
RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN cause IN ('cluster_never_reported', 'cluster_silent', 'cluster_unhealthy') THEN 'RESOURCE_STATUS_UNKNOWN'
        WHEN cause = 'domains_unserved' THEN 'RESOURCE_STATUS_DEGRADED'
        WHEN ready >= desired AND placement <> 'degraded' THEN 'RESOURCE_STATUS_HEALTHY'
        WHEN ready > 0 THEN 'RESOURCE_STATUS_DEGRADED'
        WHEN placement IN ('pending', 'deploying') THEN 'RESOURCE_STATUS_DEPLOYING'
        ELSE 'RESOURCE_STATUS_UNAVAILABLE'
    END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- An environment's status from its regions' statuses; NULL for no statuses or one that is not a region status.
CREATE FUNCTION environment_status (statuses TEXT[]) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN cardinality(statuses) = 0 OR NOT statuses <@ ARRAY[
            'RESOURCE_STATUS_HEALTHY', 'RESOURCE_STATUS_DEGRADED', 'RESOURCE_STATUS_UNAVAILABLE',
            'RESOURCE_STATUS_DEPLOYING', 'RESOURCE_STATUS_UNKNOWN'] THEN NULL
        WHEN statuses[1] = ALL(statuses) THEN statuses[1]
        WHEN statuses && ARRAY['RESOURCE_STATUS_UNAVAILABLE', 'RESOURCE_STATUS_DEGRADED'] THEN 'RESOURCE_STATUS_DEGRADED'
        WHEN 'RESOURCE_STATUS_UNKNOWN' = ANY(statuses) THEN 'RESOURCE_STATUS_UNKNOWN'
        ELSE 'RESOURCE_STATUS_DEPLOYING'
    END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A Kubernetes quantity such as 250m, .5, 1e3, 256Mi or 1Ei as a non-negative number in its base
-- unit; 0 for one that does not follow the Kubernetes quantity grammar or is negative.
CREATE FUNCTION quantity_value (quantity TEXT) RETURNS NUMERIC LANGUAGE sql IMMUTABLE AS $$
    SELECT COALESCE((
        SELECT m[1]::numeric * CASE COALESCE(m[2], '')
            WHEN '' THEN 1::numeric
            WHEN 'Ki' THEN 1024::numeric WHEN 'Mi' THEN 1048576::numeric
            WHEN 'Gi' THEN 1073741824::numeric WHEN 'Ti' THEN 1099511627776::numeric
            WHEN 'Pi' THEN 1125899906842624::numeric WHEN 'Ei' THEN 1152921504606846976::numeric
            WHEN 'n' THEN 1e-9 WHEN 'u' THEN 1e-6 WHEN 'm' THEN 1e-3
            WHEN 'k' THEN 1e3 WHEN 'M' THEN 1e6 WHEN 'G' THEN 1e9
            WHEN 'T' THEN 1e12 WHEN 'P' THEN 1e15 WHEN 'E' THEN 1e18
        END
        FROM regexp_match(
            quantity,
            '^(\+?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]{1,2})?)(Ki|Mi|Gi|Ti|Pi|Ei|n|u|m|k|M|G|T|P|E)?$'
        ) m
    ), 0)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A Kubernetes CPU quantity in millicores, capped at 9e18; 0 for one that does not parse.
CREATE FUNCTION cpu_millicores (quantity TEXT) RETURNS BIGINT LANGUAGE sql IMMUTABLE AS $$
    SELECT ceil(LEAST(quantity_value(quantity) * 1000, 9e18))::bigint
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A Kubernetes memory quantity in bytes, capped at 9e18; 0 for one that does not parse.
CREATE FUNCTION memory_bytes (quantity TEXT) RETURNS BIGINT LANGUAGE sql IMMUTABLE AS $$
    SELECT ceil(LEAST(quantity_value(quantity), 9e18))::bigint
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The placements of a deployment as the API returns them, ordered by region. A live placement is
-- one the region runs or is building, and reports the replicas the region's clusters last
-- reported, which include the previous version's while it rolls out.
CREATE FUNCTION deployment_placements_json (deployment UUID) RETURNS JSONB LANGUAGE sql STABLE AS $$
    SELECT COALESCE(jsonb_agg(jsonb_build_object(
        'clusterId', dp.cluster_id,
        'region', dp.region,
        'replicas', dp.replicas,
        'status', dp.status,
        'image', dp.image,
        'startedAt', dp.started_at,
        'completedAt', dp.completed_at,
        'supersededBy', dp.superseded_by,
        'previous', (
            SELECT older.id FROM deployments older
            JOIN deployment_placements odp ON odp.deployment_id = older.id
            WHERE older.resource_id = d.resource_id AND older.environment_id = d.environment_id
              AND odp.region = dp.region AND (older.created_at, older.id) < (d.created_at, d.id)
            ORDER BY older.created_at DESC, older.id DESC LIMIT 1),
        'live', l.live,
        'revision', CASE WHEN p.deployment_id = dp.deployment_id THEN p.desired_revision ELSE COALESCE((
            SELECT max(ro.revision) FROM deployment_rollouts ro
            WHERE ro.deployment_id = dp.deployment_id AND ro.cluster_id = dp.cluster_id), 0) END,
        'rollout', COALESCE((
            SELECT ro.snapshot FROM deployment_rollouts ro
            WHERE ro.deployment_id = dp.deployment_id AND ro.cluster_id = dp.cluster_id
            ORDER BY ro.revision DESC LIMIT 1), '{}'::jsonb),
        'observed', CASE WHEN NOT l.live THEN '[]'::jsonb ELSE COALESCE((
            SELECT jsonb_path_query_array(jsonb_agg(o.replicas ORDER BY o.placement_id), '$[*][*]')
            FROM observed_placement_rollouts o
            WHERE o.resource_id = d.resource_id AND o.environment_id = d.environment_id
              AND o.region = dp.region), '[]'::jsonb) END
    ) ORDER BY dp.region, dp.cluster_id), '[]'::jsonb)
    FROM deployment_placements dp
    JOIN deployments d ON d.id = dp.deployment_id
    LEFT JOIN placements p ON p.resource_id = d.resource_id AND p.environment_id = d.environment_id
        AND p.cluster_id = dp.cluster_id AND NOT p.desired_deleted
    CROSS JOIN LATERAL (SELECT EXISTS (
        SELECT 1 FROM live_deployment_placements l
        WHERE l.resource_id = d.resource_id AND l.environment_id = d.environment_id
          AND l.cluster_id = dp.cluster_id AND l.deployment_id = dp.deployment_id) AS live) l
    WHERE dp.deployment_id = deployment
$$;
-- +goose StatementEnd

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

CREATE INDEX idx_placement_windows_workspace_id ON placement_windows (workspace_id);

CREATE TABLE
    builds (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        resource_id UUID NOT NULL REFERENCES resources (id) ON DELETE CASCADE,
        -- Historical context survives environment deletion, like events.environment_id.
        origin_environment_id UUID,
        cluster_id UUID REFERENCES clusters (id) ON DELETE SET NULL,
        status build_status NOT NULL,
        source_type TEXT NOT NULL CHECK (source_type IN ('upload')),
        stage TEXT NOT NULL DEFAULT '',
        source_key TEXT NOT NULL UNIQUE,
        source_size BIGINT NOT NULL,
        dockerfile_path TEXT NOT NULL,
        context TEXT NOT NULL DEFAULT '.',
        git_commit TEXT,
        git_branch TEXT,
        git_commit_message TEXT,
        image_repository TEXT NOT NULL,
        platform TEXT NOT NULL CHECK (platform IN ('linux/amd64', 'linux/arm64')),
        image_digest TEXT,
        cache_digest TEXT,
        message TEXT NOT NULL DEFAULT '',
        created_by UUID NOT NULL,
        -- Set when an API token created the build; created_by records the same token ID.
        api_token_id UUID,
        method deployment_method NOT NULL,
        -- the W3C trace context of the request that created the build
        traceparent TEXT,
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

CREATE INDEX idx_builds_cluster_id ON builds (cluster_id);

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

CREATE TABLE
    registry_credentials (
        id UUID PRIMARY KEY DEFAULT uuidv7 (),
        kind TEXT NOT NULL CHECK (kind IN ('pull', 'build')),
        cluster_id UUID NOT NULL REFERENCES clusters (id) ON DELETE CASCADE,
        workspace_id UUID NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
        build_id UUID REFERENCES builds (id) ON DELETE CASCADE,
        secret_hash BYTEA NOT NULL UNIQUE,
        expires_at TIMESTAMPTZ,
        created_at TIMESTAMPTZ NOT NULL DEFAULT NOW (),
        CHECK (
            (
                kind = 'pull'
                AND build_id IS NULL
                AND expires_at IS NOT NULL
            )
            OR (
                kind = 'build'
                AND build_id IS NOT NULL
                AND expires_at IS NULL
            )
        )
    );

CREATE INDEX idx_registry_credentials_expires_at ON registry_credentials (expires_at);

CREATE INDEX idx_registry_credentials_cluster_id ON registry_credentials (cluster_id);

CREATE INDEX idx_registry_credentials_build_id ON registry_credentials (build_id);

CREATE INDEX idx_registry_credentials_workspace_id ON registry_credentials (workspace_id);

-- +goose StatementBegin
CREATE FUNCTION notify_deployment_change() RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify('deployment_activity', NEW.id::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER deployment_change AFTER INSERT OR UPDATE ON deployments
FOR EACH ROW EXECUTE FUNCTION notify_deployment_change();

-- +goose StatementBegin
CREATE FUNCTION notify_deployment_placement_change() RETURNS trigger AS $$
BEGIN
  PERFORM pg_notify('deployment_activity', NEW.deployment_id::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER deployment_placement_change AFTER INSERT OR UPDATE ON deployment_placements
FOR EACH ROW EXECUTE FUNCTION notify_deployment_placement_change();

-- +goose Down
DROP TABLE IF EXISTS registry_credentials;
DROP TABLE IF EXISTS builds;
DROP FUNCTION IF EXISTS deployment_placements_json;
DROP FUNCTION IF EXISTS memory_bytes;
DROP FUNCTION IF EXISTS cpu_millicores;
DROP FUNCTION IF EXISTS quantity_value;
DROP FUNCTION IF EXISTS environment_status;
DROP FUNCTION IF EXISTS region_status;
DROP FUNCTION IF EXISTS region_status_cause;
DROP VIEW IF EXISTS resource_region_inputs;
DROP VIEW IF EXISTS live_deployment_placements;
DROP VIEW IF EXISTS observed_placement_rollouts;
DROP FUNCTION IF EXISTS resource_suspended;
DROP TABLE IF EXISTS placement_windows;
DROP TABLE IF EXISTS placements;
DROP TABLE IF EXISTS deployment_activity;
DROP TABLE IF EXISTS deployment_rollouts;
DROP FUNCTION IF EXISTS deployment_status_from_placements;
DROP TABLE IF EXISTS deployment_placements;
DROP TABLE IF EXISTS deployments;
DROP FUNCTION IF EXISTS notify_deployment_placement_change;
DROP FUNCTION IF EXISTS notify_deployment_change;
DROP TYPE IF EXISTS deployment_trigger;
DROP TYPE IF EXISTS placement_status;
DROP TYPE IF EXISTS deployment_method;
DROP TYPE IF EXISTS deployment_actor_type;
DROP TABLE IF EXISTS resource_domains;
DROP TABLE IF EXISTS secrets;
DROP TABLE IF EXISTS resources;
DROP TABLE IF EXISTS cluster_assertions;
DROP TABLE IF EXISTS clusters;
DROP TABLE IF EXISTS environment_keys;
DROP TABLE IF EXISTS environments;
DROP TYPE IF EXISTS cluster_role;
DROP TYPE IF EXISTS domain_source;
DROP TYPE IF EXISTS build_status;
DROP TYPE IF EXISTS resource_type;
DROP TYPE IF EXISTS deployment_status;
