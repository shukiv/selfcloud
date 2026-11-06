-- runner: foreign_keys=off
-- Background imports, the archive source (design section 15, decision 92, slice im1). Three new tables, a purpose on
-- uploads (an archive uploaded for an import is not a file), the switch of the archive source per account, and the four
-- import events. The two event tables are rebuilt with the new list, as 0044 did, so their rows and indexes are kept.

-- An upload made for an import (purpose 'import') becomes an archive when it completes, and no node is made of it.
ALTER TABLE uploads ADD COLUMN purpose TEXT NOT NULL DEFAULT '' CHECK (purpose IN ('', 'import'));

-- One row per import. job_id is the jobs row that runs it now; a resume or a cancel queues a new one. The import keeps
-- its staging folder and its archive (archive_blob_id) until it ends, and cleanup empties both.
CREATE TABLE import_jobs (
    id              TEXT    PRIMARY KEY,                          -- imp_...
    job_id          TEXT    NOT NULL,                             -- the jobs row that runs it
    account_id      TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id         TEXT    NOT NULL,                             -- who started it
    source          TEXT    NOT NULL CHECK (source IN ('archive')),
    folder_id       TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    parent_id       TEXT    REFERENCES nodes (id) ON DELETE CASCADE, -- target directory; NULL is the folder's root
    on_conflict     TEXT    NOT NULL CHECK (on_conflict IN ('fail', 'rename', 'replace')),
    state           TEXT    NOT NULL CHECK (state IN ('queued', 'running', 'paused', 'cancelled', 'done', 'failed')),
    phase           TEXT    NOT NULL DEFAULT 'scan' CHECK (phase IN ('scan', 'stage', 'publish', 'cleanup', 'done')),
    pause_reason    TEXT    NOT NULL DEFAULT '',                  -- quota_exceeded or storage_full when the job paused itself
    error_code      TEXT    NOT NULL DEFAULT '',                  -- why it failed or was refused, as an API code
    failure         TEXT    NOT NULL DEFAULT '',                  -- the text of the failure, for the record and the job
    staging_folder_id TEXT  NOT NULL DEFAULT '',
    staging_root_id TEXT    NOT NULL DEFAULT '',
    archive_blob_id TEXT    NOT NULL DEFAULT '',
    archive_sha256  TEXT    NOT NULL DEFAULT '',
    archive_format  TEXT    NOT NULL DEFAULT '' CHECK (archive_format IN ('', 'zip', 'tar', 'tar.gz')),
    entries         INTEGER NOT NULL DEFAULT 0,                   -- counted by the scan
    declared_bytes  INTEGER NOT NULL DEFAULT 0,                   -- the sum of the entries' sizes, as the scan read them
    cursor          INTEGER NOT NULL DEFAULT 0,                   -- how many archive entries are finished (resume point)
    reserved_bytes  INTEGER NOT NULL DEFAULT 0,                   -- held against the quota by the entry being written
    archive_reserved INTEGER NOT NULL DEFAULT 0 CHECK (archive_reserved >= 0), -- the archive's length, held against the staging folder until the import ends (round 3, F1)
    files           INTEGER NOT NULL DEFAULT 0,
    bytes           INTEGER NOT NULL DEFAULT 0,
    skipped         INTEGER NOT NULL DEFAULT 0,
    actor           TEXT    NOT NULL DEFAULT '',                  -- the caller, as JSON; the policy checks use its fields
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    finished_at     INTEGER
) STRICT;

CREATE INDEX import_jobs_account ON import_jobs (account_id, user_id, state);
CREATE INDEX import_jobs_finished ON import_jobs (finished_at);

-- One row per entry that was written or skipped, written in the transaction that finishes the entry, so a restart keeps
-- every finished entry. seq is the order the rows were made; path is the entry's name inside the archive, and it is
-- what a later entry looks its parent up by.
CREATE TABLE import_items (
    import_id  TEXT    NOT NULL REFERENCES import_jobs (id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    path       TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    kind       TEXT    NOT NULL CHECK (kind IN ('file', 'dir')),
    state      TEXT    NOT NULL CHECK (state IN ('done', 'skipped')),
    node_id    TEXT    NOT NULL DEFAULT '',
    reuses     TEXT    NOT NULL DEFAULT '',
    bytes      INTEGER NOT NULL DEFAULT 0,
    error_code TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    PRIMARY KEY (import_id, seq)
) STRICT;

CREATE INDEX import_items_path ON import_items (import_id, path);

-- The imports' memory of what they brought in (6.4): a file is imported once per account. source_id is the archive's
-- SHA-256 and the entry's path, so the same archive imported twice copies nothing twice. It is not a job record: the
-- sweep never removes it, and the node it names may be trashed or deleted, which the import checks before it skips.
CREATE TABLE import_sources (
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    source     TEXT    NOT NULL,
    source_id  TEXT    NOT NULL,
    node_id    TEXT    NOT NULL,
    import_id  TEXT    NOT NULL,
    PRIMARY KEY (account_id, source, source_id)
) STRICT;

-- An account's switch for the archive source. A row with archive 0 turns it off; no row means on (the default).
CREATE TABLE import_switches (
    account_id TEXT    PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    archive    INTEGER NOT NULL CHECK (archive IN (0, 1))
) STRICT;

-- An archive the job holds is not an orphan while the job exists: the sweep must not remove it.
-- (The sweep reads blobs with no node; the check is in the store's OrphanUnreferenced query.)

CREATE TABLE webhook_endpoint_events_new (
    endpoint_id TEXT NOT NULL,
    account_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    event       TEXT NOT NULL CHECK (event IN ('user.created', 'user.verified', 'file.created', 'file.updated',
                    'file.deleted', 'file.moved', 'file.restored', 'upload.completed', 'quota.warning', 'quota.exceeded',
                    'webhook.test', 'comment.created', 'comment.updated', 'comment.deleted', 'comment.resolved',
                    'comment.reopened', 'import.started', 'import.completed', 'import.failed', 'import.cancelled')),
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
                   'webhook.test', 'comment.created', 'comment.updated', 'comment.deleted', 'comment.resolved',
                   'comment.reopened', 'import.started', 'import.completed', 'import.failed', 'import.cancelled')),
    payload    TEXT    NOT NULL CHECK (length(payload) <= 16384),
    created_at INTEGER NOT NULL
) STRICT;

INSERT INTO webhook_events_new (id, event_id, account_id, type, payload, created_at)
    SELECT id, event_id, account_id, type, payload, created_at FROM webhook_events;

-- The ID counter carries over, as in 0021 and 0044: AUTOINCREMENT must not reissue the ID of a deleted event.
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'webhook_events'), 0))
    WHERE name = 'webhook_events_new';
INSERT INTO sqlite_sequence (name, seq)
    SELECT 'webhook_events_new', seq FROM sqlite_sequence WHERE name = 'webhook_events'
    AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'webhook_events_new');

DROP TABLE webhook_events;

ALTER TABLE webhook_events_new RENAME TO webhook_events;

CREATE INDEX webhook_events_account ON webhook_events (account_id, id);
