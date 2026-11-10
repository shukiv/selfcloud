-- Compress to a zip in the drive (decision 120). This file adds the bundle tables only.
--
-- A bundle is made by a job of kind node.bundle whose target is the bundle's ID. The job reads the selected items as a
-- snapshot first (bundle_items), then writes the zip through the blob writer, and publishes it in one write.
--
-- state is pending while the job makes the file; cancelling once the user asked to stop; ready once the new node is in
-- place; cancelled or failed otherwise. The states are changed by compare-and-set only.
-- phase is snapshot until the snapshot is complete, then write. The snapshot-end write sets it, and takes the
-- reservation in the same write (reserved_bytes is the snapshot's byte total). written_bytes is what the writer had
-- written when its turn last ended; the disk rule counts reserved_bytes - written_bytes while the bundle is unfinished.
-- Times are Unix milliseconds.

CREATE TABLE bundle_jobs (
    id             TEXT    PRIMARY KEY CHECK (length(id) BETWEEN 5 AND 64), -- bnd_...
    job_id         TEXT    NOT NULL UNIQUE REFERENCES jobs (id) ON DELETE CASCADE,
    account_id     TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id        TEXT    NOT NULL CHECK (length(user_id) BETWEEN 1 AND 64), -- the maker; the only one who reads it
    folder_id      TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE, -- the folder the zip is saved in
    parent_id      TEXT    NOT NULL DEFAULT '', -- the target directory; '' is the folder's root
    name           TEXT    NOT NULL CHECK (length(CAST(name AS BLOB)) BETWEEN 1 AND 255), -- bytes, as design 2.1 says
    name_key       TEXT    NOT NULL CHECK (length(CAST(name_key AS BLOB)) BETWEEN 1 AND 255),
    state          TEXT    NOT NULL CHECK (state IN ('pending', 'cancelling', 'ready', 'cancelled', 'failed')),
    phase          TEXT    NOT NULL DEFAULT 'snapshot' CHECK (phase IN ('snapshot', 'write')),
    snapshot_count INTEGER NOT NULL DEFAULT 0 CHECK (snapshot_count >= 0),
    snapshot_bytes INTEGER NOT NULL DEFAULT 0 CHECK (snapshot_bytes >= 0),
    cursor         INTEGER NOT NULL DEFAULT 0 CHECK (cursor >= 0),
    reserved_bytes INTEGER NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
    written_bytes  INTEGER NOT NULL DEFAULT 0 CHECK (written_bytes >= 0),
    entries_done   INTEGER NOT NULL DEFAULT 0 CHECK (entries_done >= 0),
    files          INTEGER NOT NULL DEFAULT 0 CHECK (files >= 0),
    dirs           INTEGER NOT NULL DEFAULT 0 CHECK (dirs >= 0),
    bytes          INTEGER NOT NULL DEFAULT 0 CHECK (bytes >= 0),
    skipped        INTEGER NOT NULL DEFAULT 0 CHECK (skipped >= 0),
    node_id        TEXT    NOT NULL DEFAULT '', -- the new zip node, once ready
    error_code     TEXT    NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    error_reason   TEXT    NOT NULL DEFAULT '' CHECK (length(error_reason) <= 64),
    actor          TEXT    NOT NULL DEFAULT '', -- the caller, as JSON, as the job's policy checks read it
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    finished_at    INTEGER
) STRICT;

CREATE INDEX bundle_jobs_account_state ON bundle_jobs (account_id, state);

-- The disk rule sums what the unfinished bundles hold, on every upload check. Only unfinished rows are indexed, so the
-- index stays small while finished bundles (which have no expiry) grow.
CREATE INDEX bundle_jobs_unwritten ON bundle_jobs (account_id, reserved_bytes, written_bytes)
    WHERE state IN ('pending', 'cancelling');

-- bundle_roots holds the items the caller asked for, in order. node_id is not a foreign key: a trashed or purged root
-- is found by the job, which fails it with a reason.
CREATE TABLE bundle_roots (
    bundle_id TEXT    NOT NULL REFERENCES bundle_jobs (id) ON DELETE CASCADE,
    seq       INTEGER NOT NULL CHECK (seq >= 0),
    node_id   TEXT    NOT NULL CHECK (length(node_id) BETWEEN 1 AND 64),
    PRIMARY KEY (bundle_id, seq)
) STRICT;

-- bundle_items is the snapshot: every entry the zip will hold, parents first, by seq (seq 1 on). parent_seq is the
-- parent entry's seq, 0 for an entry directly under a root; parent_id is the parent node when the snapshot was read,
-- so a change of parent fails the job. path is the entry's name inside the zip. blob_id is the blob a file reads, and
-- a change of its contents fails the job (a checksum mismatch).
CREATE TABLE bundle_items (
    bundle_id  TEXT    NOT NULL REFERENCES bundle_jobs (id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL CHECK (seq >= 1),
    node_id    TEXT    NOT NULL CHECK (length(node_id) BETWEEN 1 AND 64),
    parent_seq INTEGER NOT NULL DEFAULT 0 CHECK (parent_seq >= 0),
    parent_id  TEXT    NOT NULL DEFAULT '',
    path       TEXT    NOT NULL CHECK (length(path) BETWEEN 1 AND 4096),
    kind       TEXT    NOT NULL CHECK (kind IN ('file', 'dir')),
    size       INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
    blob_id    TEXT    NOT NULL DEFAULT '',
    mod_at     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (bundle_id, seq)
) STRICT;
