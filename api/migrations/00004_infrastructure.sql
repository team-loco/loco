-- +goose Up
CREATE TABLE infra_plans (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    environment_id UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    stack_name TEXT NOT NULL,
    expected_revision BIGINT NOT NULL,
    manifest_digest TEXT NOT NULL,
    source_digest TEXT NOT NULL,
    payload BYTEA NOT NULL,
    plan JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE infra_applies (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    plan_id UUID NOT NULL UNIQUE REFERENCES infra_plans(id),
    deployment_ids UUID[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE infra_secret_versions (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    environment_id UUID NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    ciphertext BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX infra_secret_versions_lookup ON infra_secret_versions(environment_id, name, created_at DESC, id DESC);

-- +goose StatementBegin
CREATE FUNCTION bump_environment_intent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target UUID;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF TG_TABLE_NAME = 'resources' THEN
            IF NEW.name = OLD.name AND NEW.spec = OLD.spec AND NEW.description = OLD.description AND NEW.variable_values IS NOT DISTINCT FROM OLD.variable_values THEN
                RETURN NULL;
            END IF;
        END IF;
        IF TG_TABLE_NAME = 'placements' THEN
            IF NEW.desired_spec IS NOT DISTINCT FROM OLD.desired_spec AND NEW.desired_deleted = OLD.desired_deleted AND NEW.deployment_id IS NOT DISTINCT FROM OLD.deployment_id THEN
                RETURN NULL;
            END IF;
        END IF;
        IF TG_TABLE_NAME = 'resource_regions' THEN
            IF NEW.region = OLD.region AND NEW.is_primary = OLD.is_primary THEN RETURN NULL; END IF;
        END IF;
    END IF;
    IF TG_TABLE_NAME = 'resources' OR TG_TABLE_NAME = 'infra_stacks' OR TG_TABLE_NAME = 'infra_secret_versions' THEN
        IF TG_OP = 'DELETE' THEN target := OLD.environment_id;
        ELSE target := NEW.environment_id;
        END IF;
    ELSE
        IF TG_OP = 'DELETE' THEN
            SELECT environment_id INTO target FROM resources WHERE id = OLD.resource_id;
        ELSE
            SELECT environment_id INTO target FROM resources WHERE id = NEW.resource_id;
        END IF;
    END IF;
    UPDATE environments SET intent_revision = intent_revision + 1 WHERE id = target;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER resources_intent AFTER INSERT OR UPDATE OR DELETE ON resources
FOR EACH ROW EXECUTE FUNCTION bump_environment_intent();
CREATE TRIGGER regions_intent AFTER INSERT OR UPDATE OR DELETE ON resource_regions
FOR EACH ROW EXECUTE FUNCTION bump_environment_intent();
CREATE TRIGGER domains_intent AFTER INSERT OR UPDATE OR DELETE ON resource_domains
FOR EACH ROW EXECUTE FUNCTION bump_environment_intent();
CREATE TRIGGER placements_intent AFTER INSERT OR UPDATE OR DELETE ON placements
FOR EACH ROW EXECUTE FUNCTION bump_environment_intent();
CREATE TRIGGER stacks_intent AFTER INSERT OR UPDATE OR DELETE ON infra_stacks
FOR EACH ROW EXECUTE FUNCTION bump_environment_intent();
CREATE TRIGGER secrets_intent AFTER INSERT ON infra_secret_versions
FOR EACH ROW EXECUTE FUNCTION bump_environment_intent();

-- +goose Down
DROP TRIGGER secrets_intent ON infra_secret_versions;
DROP TRIGGER stacks_intent ON infra_stacks;
DROP TRIGGER placements_intent ON placements;
DROP TRIGGER domains_intent ON resource_domains;
DROP TRIGGER regions_intent ON resource_regions;
DROP TRIGGER resources_intent ON resources;
DROP FUNCTION bump_environment_intent();
DROP TABLE infra_applies;
DROP TABLE infra_plans;
DROP TABLE infra_secret_versions;
