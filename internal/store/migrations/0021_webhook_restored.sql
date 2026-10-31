-- runner: foreign_keys=off
-- Slice j2 follow-up: file.restored joins the event list (decision 27). SQLite cannot change a CHECK, so the two
-- tables that hold the list are rebuilt with the new list, and their rows and indexes are kept. webhook_deliveries
-- refers to webhook_events, so foreign keys are off for this migration, and the rebuilt table takes the old name.

CREATE TABLE webhook_endpoint_events_new (
    endpoint_id TEXT NOT NULL,
    account_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    event       TEXT NOT NULL CHECK (event IN ('user.created', 'user.verified', 'file.created', 'file.updated',
                    'file.deleted', 'file.moved', 'file.restored', 'upload.completed', 'quota.warning', 'quota.exceeded',
                    'webhook.test')),
    PRIMARY KEY (endpoint_id, event),
    FOREIGN KEY (endpoint_id, account_id) REFERENCES webhook_endpoints (id, account_id) ON DELETE CASCADE
) STRICT;

INSERT INTO webhook_endpoint_events_new (endpoint_id, account_id, event)
    SELECT endpoint_id, account_id, event FROM webhook_endpoint_events;

DROP TABLE webhook_endpoint_events;

ALTER TABLE webhook_endpoint_events_new RENAME TO webhook_endpoint_events;

CREATE INDEX webhook_endpoint_events_account ON webhook_endpoint_events (account_id);

CREATE TABLE webhook_events_new (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id   TEXT    NOT NULL UNIQUE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    type       TEXT    NOT NULL CHECK (type IN ('user.created', 'user.verified', 'file.created', 'file.updated',
                   'file.deleted', 'file.moved', 'file.restored', 'upload.completed', 'quota.warning', 'quota.exceeded',
                   'webhook.test')),
    payload    TEXT    NOT NULL CHECK (length(payload) <= 16384),
    created_at INTEGER NOT NULL
) STRICT;

INSERT INTO webhook_events_new (id, event_id, account_id, type, payload, created_at)
    SELECT id, event_id, account_id, type, payload, created_at FROM webhook_events;

-- The ID counter carries over. AUTOINCREMENT hands out IDs above the highest one it has issued, and the copy holds only
-- the rows that remain, so without this an ID of a deleted event (the top rows the sweep removes) would be issued again.
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'webhook_events'), 0))
    WHERE name = 'webhook_events_new';
INSERT INTO sqlite_sequence (name, seq)
    SELECT 'webhook_events_new', seq FROM sqlite_sequence WHERE name = 'webhook_events'
    AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'webhook_events_new');

DROP TABLE webhook_events;

ALTER TABLE webhook_events_new RENAME TO webhook_events;

CREATE INDEX webhook_events_account ON webhook_events (account_id, id);
