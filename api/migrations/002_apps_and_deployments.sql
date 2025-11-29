-- Deployment status enum
CREATE TYPE deployment_status AS ENUM ('pending', 'running', 'succeeded', 'failed', 'canceled');

-- App status enum
CREATE TYPE app_status AS ENUM ('available', 'progressing', 'degraded', 'unavailable', 'idle');

-- Clusters table
CREATE TABLE clusters (
    id BIGSERIAL PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    region TEXT NOT NULL,
    provider TEXT NOT NULL,
    is_active BOOLEAN DEFAULT true,
    endpoint TEXT,
    health_status TEXT DEFAULT 'healthy' CHECK (health_status IN ('healthy', 'unhealthy', 'degraded')),
    last_health_check TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX idx_clusters_region ON clusters (region);
CREATE INDEX idx_clusters_provider ON clusters (provider);
CREATE INDEX idx_clusters_is_active ON clusters (is_active);

-- Apps table
CREATE TABLE apps (
    id BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    cluster_id BIGINT NOT NULL REFERENCES clusters(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    type INT NOT NULL,
    subdomain TEXT NOT NULL,
    domain TEXT NOT NULL DEFAULT 'loco.deploy-app.com',
    status app_status DEFAULT 'idle',
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE (workspace_id, name),
    UNIQUE (subdomain, domain),
    UNIQUE (cluster_id, namespace)
);

CREATE INDEX idx_apps_workspace_id ON apps (workspace_id);
CREATE INDEX idx_apps_cluster_id ON apps (cluster_id);
CREATE INDEX idx_apps_subdomain_domain ON apps (subdomain, domain);


-- Deployments table
CREATE TABLE deployments (
    id BIGSERIAL PRIMARY KEY,
    app_id BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    cluster_id BIGINT NOT NULL REFERENCES clusters(id) ON DELETE RESTRICT,
    image TEXT NOT NULL,
    replicas INT NOT NULL DEFAULT 1,
    status deployment_status NOT NULL DEFAULT 'pending',
    is_current BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT,
    message TEXT,
    config JSONB DEFAULT '{}'::jsonb,
    schema_version INT DEFAULT 1,
    created_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_deployments_app_id ON deployments (app_id);
CREATE INDEX idx_deployments_cluster_id ON deployments (cluster_id);
CREATE INDEX idx_deployments_is_current ON deployments (app_id, is_current) WHERE is_current = true;