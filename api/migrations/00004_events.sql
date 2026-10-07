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

-- +goose Down
DROP TABLE IF EXISTS events;
DROP FUNCTION IF EXISTS events_reject_update();
DROP FUNCTION IF EXISTS events_notify();
