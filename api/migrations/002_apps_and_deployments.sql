-- Deployment status enum
CREATE TYPE deployment_status AS ENUM ('pending', 'running', 'succeeded', 'failed', 'canceled');

-- Resource status enum
CREATE TYPE resource_status AS ENUM ('available', 'progressing', 'degraded', 'unavailable', 'idle');

-- Resource type enum
CREATE TYPE resource_type AS ENUM ('service', 'worker', 'database', 'cache', 'queue', 'blob');

-- Domain source enum (who manages the domain)
CREATE TYPE domain_source AS ENUM ('platform_provided', 'user_provided');

-- Region intent status enum
CREATE TYPE region_intent_status AS ENUM ('desired', 'provisioning', 'active', 'degraded', 'removing', 'failed');

-- Clusters table
CREATE TABLE clusters (
    id BIGSERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    region TEXT NOT NULL,
    provider TEXT NOT NULL,
    is_active BOOLEAN NOT NULL,
    is_default BOOLEAN NOT NULL,
    endpoint TEXT,
    health_status TEXT CHECK (health_status IN ('healthy', 'unhealthy', 'degraded')),
    last_health_check TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_clusters_region ON clusters (region);
CREATE INDEX idx_clusters_is_active ON clusters (is_active);

-- Platform domains (loco-provided base domains)
CREATE TABLE platform_domains (
    id BIGSERIAL PRIMARY KEY,
    domain TEXT NOT NULL UNIQUE,
    is_active BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Resources table
CREATE TABLE resources (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    type resource_type NOT NULL,
    description TEXT NOT NULL,
    status resource_status NOT NULL,
    spec JSONB NOT NULL,
    spec_version INT NOT NULL,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (workspace_id, name)
);

CREATE INDEX idx_resources_workspace_id ON resources (workspace_id);

-- Resource regions table (declarative intent)
CREATE TABLE resource_regions (
    id BIGSERIAL PRIMARY KEY,
    resource_id BIGINT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    region TEXT NOT NULL,
    is_primary BOOLEAN NOT NULL,
    status region_intent_status NOT NULL,
    last_error TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (resource_id, region)
);

CREATE INDEX idx_resource_regions_resource_id ON resource_regions (resource_id);
CREATE INDEX idx_resource_regions_region ON resource_regions (region);

-- Enforce max 1 primary region per resource
CREATE UNIQUE INDEX uniq_resource_primary_region
  ON resource_regions(resource_id)
  WHERE is_primary = true;

CREATE TABLE resource_domains (
    id BIGSERIAL PRIMARY KEY,
    resource_id BIGINT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    domain TEXT NOT NULL UNIQUE,
    domain_source domain_source NOT NULL,
    subdomain_label TEXT,
    platform_domain_id BIGINT REFERENCES platform_domains(id),
    is_primary BOOLEAN NOT NULL,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT domain_source_check CHECK (
        (domain_source = 'platform_provided' AND subdomain_label IS NOT NULL AND platform_domain_id IS NOT NULL)
        OR
        (domain_source = 'user_provided' AND subdomain_label IS NULL AND platform_domain_id IS NULL)
    )
);

CREATE INDEX idx_resource_domains_resource_id ON resource_domains(resource_id);
CREATE INDEX idx_resource_domains_domain ON resource_domains(domain);

-- Enforce max 1 primary domain per resource
CREATE UNIQUE INDEX uniq_resource_primary_domain
  ON resource_domains(resource_id)
  WHERE is_primary = true;

-- Ensure platform subdomain uniqueness
CREATE UNIQUE INDEX uniq_platform_subdomain
  ON resource_domains(platform_domain_id, subdomain_label)
  WHERE domain_source = 'platform_provided';



-- Deployments table (immutable, single-region)
CREATE TABLE deployments (
    id BIGSERIAL PRIMARY KEY,
    resource_id BIGINT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    cluster_id BIGINT NOT NULL REFERENCES clusters(id) ON DELETE RESTRICT,
    image TEXT NOT NULL,
    replicas INT NOT NULL,
    status deployment_status NOT NULL,
    is_active BOOLEAN NOT NULL,
    message TEXT,
    spec JSONB NOT NULL,
    spec_version INT NOT NULL,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_deployments_resource_id ON deployments (resource_id);
CREATE INDEX idx_deployments_cluster_id ON deployments (cluster_id);
CREATE INDEX idx_deployments_is_active ON deployments (resource_id, is_active) WHERE is_active = true;
CREATE INDEX idx_deployments_status_created_at ON deployments (status, created_at DESC);
