-- +goose Up
CREATE TABLE events (
    seq BIGSERIAL PRIMARY KEY,
    stream_position BIGINT,
    id UUID NOT NULL UNIQUE DEFAULT uuidv7(),
    type TEXT NOT NULL,
    org_id UUID,
    workspace_id UUID,
    resource_id UUID,
    environment_id UUID,
    deployment_id UUID,
    actor_type TEXT NOT NULL,
    actor_id UUID,
    actor_name TEXT,
    actor_email TEXT,
    actor_token_type TEXT,
    -- The actor changed the organization through an installation role, not as one of its members.
    acted_as_operator BOOLEAN NOT NULL DEFAULT false,
    subject_type TEXT,
    subject_id UUID,
    request_id TEXT,
    traceparent TEXT,
    data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX events_org_actor_seq_idx ON events (org_id, actor_id, seq DESC);
CREATE INDEX events_org_actor_type_seq_idx ON events (org_id, actor_type, seq DESC);
CREATE INDEX events_org_request_idx ON events (org_id, request_id);

CREATE INDEX events_org_type_seq_idx ON events (org_id, type, seq DESC);
CREATE INDEX events_workspace_type_seq_idx ON events (workspace_id, type, seq DESC);
CREATE INDEX events_org_environment_seq_idx ON events (org_id, environment_id, seq DESC);

CREATE INDEX events_workspace_seq_idx ON events (workspace_id, seq DESC);
CREATE INDEX events_resource_seq_idx ON events (resource_id, seq DESC);
CREATE INDEX events_org_seq_idx ON events (org_id, seq DESC);
CREATE INDEX events_created_at_idx ON events (created_at);
CREATE INDEX events_unpublished_seq_idx ON events (seq) WHERE stream_position IS NULL;
CREATE UNIQUE INDEX events_stream_position_idx ON events (stream_position);

CREATE TABLE event_stream_counter (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    position BIGINT NOT NULL DEFAULT 0
);
INSERT INTO event_stream_counter DEFAULT VALUES;

-- +goose StatementBegin
CREATE FUNCTION events_reject_update() RETURNS trigger AS $$
BEGIN
    IF OLD.stream_position IS NULL AND NEW.stream_position > 0
        AND (to_jsonb(OLD) - 'stream_position') = (to_jsonb(NEW) - 'stream_position') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'events are append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER events_append_only
BEFORE UPDATE ON events
FOR EACH ROW EXECUTE FUNCTION events_reject_update();

CREATE TYPE webhook_kind AS ENUM ('workspace', 'install');

CREATE TABLE webhooks (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    kind webhook_kind NOT NULL,
    workspace_id UUID REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT '' CHECK (char_length(name) <= 100),
    url TEXT NOT NULL,
    -- The webhook's data key, wrapped by the secret key provider.
    provider TEXT NOT NULL,
    kek_id TEXT NOT NULL,
    wrapped_dek BYTEA NOT NULL,
    -- The signing secret: an AES-256-GCM nonce followed by the ciphertext under the data key.
    secret BYTEA NOT NULL,
    -- The secret a rotation replaced, sealed the same way; deliveries are also signed with it until it expires.
    previous_secret BYTEA,
    previous_secret_expires_at TIMESTAMPTZ,
    event_types TEXT[] NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT webhooks_previous_secret CHECK ((previous_secret IS NULL) = (previous_secret_expires_at IS NULL)),
    CONSTRAINT webhooks_kind_owner CHECK (
        (kind = 'workspace' AND workspace_id IS NOT NULL)
        OR (kind = 'install' AND workspace_id IS NULL)
    )
);

CREATE INDEX webhooks_created_by_idx ON webhooks (created_by);
CREATE INDEX webhooks_workspace_idx ON webhooks (workspace_id);
CREATE UNIQUE INDEX webhooks_install_url_idx ON webhooks (url) WHERE kind = 'install';

CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    webhook_id UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_seq BIGINT NOT NULL REFERENCES events(seq) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    UNIQUE (webhook_id, event_seq)
);

CREATE VIEW webhook_delivery_results AS
SELECT d.*, (status = 'failed' OR (status = 'pending' AND COALESCE(last_error, '') <> '')) AS failing
FROM webhook_deliveries d;

CREATE INDEX webhook_deliveries_event_seq_idx ON webhook_deliveries (event_seq);
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_webhook_idx ON webhook_deliveries (webhook_id, created_at DESC, id DESC);
CREATE INDEX webhook_deliveries_webhook_status_idx ON webhook_deliveries (webhook_id, status, created_at DESC, id DESC);

-- +goose StatementBegin
CREATE FUNCTION events_enqueue_webhooks() RETURNS trigger AS $$
BEGIN
    -- A test event goes only to the webhook it tests, whatever that webhook subscribes to.
    INSERT INTO webhook_deliveries (webhook_id, event_seq)
    SELECT w.id, NEW.seq
    FROM webhooks w
    WHERE CASE
        WHEN NEW.type = 'webhook.test' THEN w.id = NEW.subject_id
        ELSE (w.kind = 'install' OR (w.kind = 'workspace' AND w.workspace_id = NEW.workspace_id))
            AND (cardinality(w.event_types) = 0 OR NEW.type = ANY(w.event_types))
    END;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER events_enqueue_webhooks_insert
AFTER INSERT ON events
FOR EACH ROW EXECUTE FUNCTION events_enqueue_webhooks();

-- +goose StatementBegin
-- The region an event names: its region, or its regions joined.
CREATE FUNCTION event_region (data JSONB) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT COALESCE(NULLIF(data->>'region', ''), array_to_string(ARRAY(SELECT jsonb_array_elements_text(
    CASE WHEN jsonb_typeof(data->'regions') = 'array' THEN data->'regions' END)), ', '), '')
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- How the dashboard's Events view grades an event type.
CREATE FUNCTION event_severity (type TEXT) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE
    WHEN type IN ('build.failed', 'deployment.failed', 'deployment.crashed') THEN 'error'
    WHEN type = 'resource.degraded' THEN 'warning'
    ELSE 'normal'
  END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The label the dashboard shows for an event; types without one show the type.
CREATE FUNCTION event_title (type TEXT, data JSONB) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE type
      WHEN 'resource.degraded' THEN 'Degraded'
      WHEN 'resource.recovered' THEN 'Recovered'
      WHEN 'resource.restarted' THEN 'Restarted'
      WHEN 'resource.scaled' THEN 'Scaled'
      WHEN 'deployment.created' THEN 'Deploy created'
      WHEN 'deployment.ready' THEN 'Deploy ready'
      WHEN 'deployment.failed' THEN 'Deploy failed'
      WHEN 'deployment.crashed' THEN 'Deploy crashed'
      WHEN 'build.created' THEN 'Build queued'
      WHEN 'build.started' THEN 'Build started'
      WHEN 'build.succeeded' THEN 'Build succeeded'
      WHEN 'build.failed' THEN 'Build failed'
      WHEN 'build.canceled' THEN 'Build canceled'
      ELSE type
    END
    || CASE
      WHEN type IN ('resource.degraded', 'resource.recovered', 'resource.restarted',
                    'deployment.ready', 'deployment.failed', 'deployment.crashed')
        AND event_region(data) <> '' THEN ' in ' || event_region(data)
      ELSE ''
    END
$$;
-- +goose StatementEnd

-- +goose Down
DROP VIEW IF EXISTS webhook_delivery_results;
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhooks;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS event_stream_counter;
DROP FUNCTION IF EXISTS events_reject_update();
DROP FUNCTION IF EXISTS events_enqueue_webhooks();
DROP TYPE IF EXISTS webhook_kind;
DROP FUNCTION IF EXISTS event_title;
DROP FUNCTION IF EXISTS event_severity;
DROP FUNCTION IF EXISTS event_region;
