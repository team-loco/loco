-- Deployment status enum
CREATE TYPE deployment_status AS ENUM ('pending', 'running', 'succeeded', 'failed', 'canceled');

-- Resource status enum
CREATE TYPE resource_status AS ENUM ('available', 'progressing', 'degraded', 'unavailable', 'idle');

-- Domain source enum (who manages the domain)
CREATE TYPE domain_source AS ENUM ('platform_provided', 'user_provided');

-- Clusters table
CREATE TABLE clusters (
    id BIGSERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    region TEXT NOT NULL,
    provider TEXT NOT NULL,
    is_active BOOLEAN DEFAULT true,
    endpoint TEXT,
    health_status TEXT DEFAULT 'healthy' CHECK (health_status IN ('healthy', 'unhealthy', 'degraded')),
    last_health_check TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX idx_clusters_region ON clusters (region);
CREATE INDEX idx_clusters_provider ON clusters (provider);
CREATE INDEX idx_clusters_is_active ON clusters (is_active);

-- Platform domains (loco-provided base domains)
CREATE TABLE platform_domains (
    id BIGSERIAL PRIMARY KEY,
    domain TEXT NOT NULL UNIQUE,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Resources table
CREATE TABLE resources (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    cluster_id BIGINT NOT NULL REFERENCES clusters(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    type INT NOT NULL,
    status resource_status DEFAULT 'idle',
    spec JSONB DEFAULT '{}'::jsonb,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (workspace_id, name)
);

CREATE INDEX idx_resources_workspace_id ON resources (workspace_id);
CREATE INDEX idx_resources_cluster_id ON resources (cluster_id);

CREATE TABLE resource_domains (
    id BIGSERIAL PRIMARY KEY,
    resource_id BIGINT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    domain TEXT NOT NULL UNIQUE,
    domain_source domain_source NOT NULL,
    subdomain_label TEXT,
    platform_domain_id BIGINT REFERENCES platform_domains(id),
    is_primary BOOLEAN NOT NULL DEFAULT false,
    
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



-- Deployments table
CREATE TABLE deployments (
    id BIGSERIAL PRIMARY KEY,
    resource_id BIGINT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    cluster_id BIGINT NOT NULL REFERENCES clusters(id) ON DELETE RESTRICT,
    image TEXT NOT NULL,
    replicas INT NOT NULL DEFAULT 1,
    status deployment_status NOT NULL DEFAULT 'pending',
    is_current BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT,
    message TEXT,
    spec JSONB DEFAULT '{}'::jsonb,
    schema_version INT DEFAULT 1,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_deployments_resource_id ON deployments (resource_id);
CREATE INDEX idx_deployments_cluster_id ON deployments (cluster_id);
CREATE INDEX idx_deployments_is_current ON deployments (resource_id, is_current) WHERE is_current = true;
