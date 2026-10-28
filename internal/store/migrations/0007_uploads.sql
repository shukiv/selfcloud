-- Resumable uploads (tus). Times are Unix milliseconds.
--
-- An upload's bytes live in uploads/<account>/<id> until the upload completes, when the file is
-- renamed to blobs/<account>/.../<blob_id>. blob_id is chosen when the upload is created, so a
-- retry after a crash finds the file whether or not the rename happened.
--
-- durable_offset is the number of bytes that are on disk and synced; it is the only offset a
-- client may resume from. hash_state is the SHA-256 state (MarshalBinary) at durable_offset.
CREATE TABLE uploads (
    id             TEXT    PRIMARY KEY,           -- upl_..., also the file name on disk
    account_id     TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    folder_id      TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    parent_id      TEXT    REFERENCES nodes (id) ON DELETE CASCADE, -- target directory by ID, or NULL
    dir_path       TEXT    NOT NULL DEFAULT '',  -- target directory by path, when parent_id is NULL
    parents        INTEGER NOT NULL DEFAULT 0 CHECK (parents IN (0, 1)),
    name           TEXT    NOT NULL,
    name_key       TEXT    NOT NULL,
    on_conflict    TEXT    NOT NULL CHECK (on_conflict IN ('fail', 'rename', 'replace')),
    mime_type      TEXT    NOT NULL,
    sha256         TEXT    NOT NULL DEFAULT '',  -- expected whole-file hex from metadata; '' if none
    length         INTEGER NOT NULL CHECK (length >= 0),
    durable_offset INTEGER NOT NULL DEFAULT 0 CHECK (durable_offset >= 0 AND durable_offset <= length),
    hash_state     BLOB,                          -- NULL at offset 0
    blob_id        TEXT    NOT NULL UNIQUE,
    created_by     TEXT    NOT NULL,
    created_kind   TEXT    NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX uploads_expiry ON uploads (expires_at);
CREATE INDEX uploads_account ON uploads (account_id);
