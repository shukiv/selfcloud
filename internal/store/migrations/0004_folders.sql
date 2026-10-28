-- Folders, the grants that share them with member users, and the file tree.
-- Times are Unix milliseconds.

CREATE TABLE folders (
    id          TEXT    PRIMARY KEY,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    external_id TEXT,
    name        TEXT    NOT NULL,
    name_key    TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (account_id, name_key)
) STRICT;

CREATE UNIQUE INDEX folders_external_id ON folders (account_id, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX folders_list ON folders (account_id, created_at, id);

-- A grant shares a folder with a member user. Owners and admins reach every folder of their
-- account without grants, so only member users of the same account can hold one; the store
-- checks that before it writes.
CREATE TABLE grants (
    folder_id  TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    level      TEXT    NOT NULL CHECK (level IN ('read', 'write')),
    created_at INTEGER NOT NULL,
    created_by TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (folder_id, user_id)
) STRICT;

CREATE INDEX grants_user ON grants (user_id, folder_id);

CREATE TABLE nodes (
    id          TEXT    PRIMARY KEY,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    folder_id   TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    parent_id   TEXT    REFERENCES nodes (id) ON DELETE CASCADE, -- NULL only for a folder's root
    name        TEXT    NOT NULL,  -- NFC; '' for a root
    name_key    TEXT    NOT NULL,  -- NFC, case-folded; unique among siblings
    kind        TEXT    NOT NULL CHECK (kind IN ('dir', 'file')),
    size        INTEGER NOT NULL DEFAULT 0,
    mime_type   TEXT    NOT NULL DEFAULT '',
    blob_id     TEXT,              -- files only; blobs are shared by copies, collected by reference
    etag        TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    modified_at INTEGER NOT NULL,
    created_by  TEXT    NOT NULL DEFAULT '',
    modified_by TEXT    NOT NULL DEFAULT ''
) STRICT;
CREATE UNIQUE INDEX nodes_child ON nodes (parent_id, name_key);
CREATE UNIQUE INDEX nodes_root ON nodes (folder_id) WHERE parent_id IS NULL;
CREATE INDEX nodes_folder ON nodes (folder_id);
CREATE INDEX nodes_blob ON nodes (blob_id) WHERE blob_id IS NOT NULL;
