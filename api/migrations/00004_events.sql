-- +goose Up
CREATE TABLE events (
    seq BIGSERIAL PRIMARY KEY,
    txid XID8 NOT NULL DEFAULT pg_current_xact_id(),
    id UUID NOT NULL UNIQUE DEFAULT uuidv7(),
    type TEXT NOT NULL,
    org_id UUID,
    workspace_id UUID,
    actor_type TEXT NOT NULL,
    actor_id UUID,
    subject_type TEXT,
    subject_id UUID,
    request_id TEXT,
    data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX events_org_seq_idx ON events (org_id, seq DESC);
CREATE INDEX events_created_at_idx ON events (created_at);
CREATE INDEX events_txid_seq_idx ON events (txid, seq);

-- +goose StatementBegin
CREATE FUNCTION events_reject_update() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'events are append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER events_append_only
BEFORE UPDATE ON events
FOR EACH ROW EXECUTE FUNCTION events_reject_update();

-- +goose StatementBegin
CREATE FUNCTION events_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('loco_events', '');
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER events_notify_insert
AFTER INSERT ON events
FOR EACH STATEMENT EXECUTE FUNCTION events_notify();

CREATE TABLE webhooks (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    secret TEXT NOT NULL,
    event_types TEXT[] NOT NULL DEFAULT '{}',
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX webhooks_workspace_idx ON webhooks (workspace_id);

CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    webhook_id UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_seq BIGINT NOT NULL REFERENCES events(seq) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed')),
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_status_code INT,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    UNIQUE (webhook_id, event_seq)
);

CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_webhook_idx ON webhook_deliveries (webhook_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION events_enqueue_webhooks() RETURNS trigger AS $$
BEGIN
    IF NEW.workspace_id IS NOT NULL THEN
        INSERT INTO webhook_deliveries (webhook_id, event_seq)
        SELECT w.id, NEW.seq
        FROM webhooks w
        WHERE w.workspace_id = NEW.workspace_id
          AND (cardinality(w.event_types) = 0 OR NEW.type = ANY(w.event_types));
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER events_enqueue_webhooks_insert
AFTER INSERT ON events
FOR EACH ROW EXECUTE FUNCTION events_enqueue_webhooks();

-- +goose Down
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhooks;
DROP TABLE IF EXISTS events;
DROP FUNCTION IF EXISTS events_reject_update();
DROP FUNCTION IF EXISTS events_notify();
DROP FUNCTION IF EXISTS events_enqueue_webhooks();
