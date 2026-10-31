-- Chunked folder copy (design section 8, decision 34).
--
-- A copy above the sync limit builds its tree in a staging folder: a folder with staged = 1, which no folder read
-- returns (store folder reads filter staged = 0, and a guard test pins it). Its nodes are invisible until the publish
-- write moves them into the destination folder. staged is a column, not a kind, so the CHECK on kind is unchanged.
--
-- copy_jobs holds one row per chunked copy: its phase, cursor, and the reservation it holds against the quota. The
-- reservation is reserved_bytes on the staging folder, and copy_jobs keeps the same number so the release happens once,
-- in the write that sets it to zero. copy_job_nodes is the snapshot of the source's subtree, parents first, by seq.
-- Times are Unix milliseconds.

ALTER TABLE folders ADD COLUMN staged INTEGER NOT NULL DEFAULT 0 CHECK (staged IN (0, 1));

CREATE INDEX folders_staged ON folders (account_id) WHERE staged = 1;

CREATE TABLE copy_jobs (
    job_id             TEXT    PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
    account_id         TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    staging_folder_id  TEXT    NOT NULL,
    staging_root_id    TEXT    NOT NULL,
    root_id            TEXT    NOT NULL,
    source_id          TEXT    NOT NULL,
    dest_folder_id     TEXT    NOT NULL,
    dest_parent_id     TEXT    NOT NULL,
    name               TEXT    NOT NULL,
    name_key           TEXT    NOT NULL,
    on_conflict        TEXT    NOT NULL CHECK (on_conflict IN ('', 'fail', 'rename', 'replace')),
    keep_sharing       INTEGER NOT NULL CHECK (keep_sharing IN (0, 1)),
    keep_restricted    INTEGER NOT NULL DEFAULT -1 CHECK (keep_restricted IN (-1, 0, 1)),
    phase              TEXT    NOT NULL CHECK (phase IN ('snapshot', 'copy', 'publish', 'cleanup', 'done')),
    snapshot_count     INTEGER NOT NULL DEFAULT 0,
    cursor             INTEGER NOT NULL DEFAULT 0,
    reserved_bytes     INTEGER NOT NULL CHECK (reserved_bytes >= 0),
    nodes              INTEGER NOT NULL DEFAULT 0,
    files              INTEGER NOT NULL DEFAULT 0,
    bytes              INTEGER NOT NULL DEFAULT 0,
    failure            TEXT    NOT NULL DEFAULT '',
    actor              TEXT    NOT NULL,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;

CREATE INDEX copy_jobs_account ON copy_jobs (account_id, phase);

CREATE TABLE copy_job_nodes (
    job_id  TEXT    NOT NULL REFERENCES copy_jobs (job_id) ON DELETE CASCADE,
    seq     INTEGER NOT NULL CHECK (seq >= 1),
    node_id TEXT    NOT NULL,
    parent_id TEXT  NOT NULL, -- the node's parent when the snapshot was read ('' for none); a change fails the copy
    PRIMARY KEY (job_id, seq)
) STRICT, WITHOUT ROWID;

-- A node appears once in a copy's snapshot: InsertCopySnapshot checks this index before it stores a row.
CREATE INDEX copy_job_nodes_node ON copy_job_nodes (job_id, node_id);
