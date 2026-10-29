-- Trash and version history (slice m). Times are Unix milliseconds.
--
-- A trashed node is an explicit trash root: only the node that was deleted carries the mark, and its
-- subtree is trashed through the ancestor walk. trashed_name_key keeps the name key the node had, so
-- a restore can put it back. While trashed, name_key holds a reserved value that names cannot make, so
-- the name is free for a new item.
ALTER TABLE nodes ADD COLUMN trashed_at INTEGER;
ALTER TABLE nodes ADD COLUMN trashed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN trashed_name_key TEXT NOT NULL DEFAULT '';
CREATE INDEX nodes_trash ON nodes (account_id, trashed_at) WHERE trashed_at IS NOT NULL;

-- A version is an earlier content of a file, kept because the file's content was replaced. Its blob
-- is referenced by the row, so the collector keeps it. A version counts toward the account's usage.
CREATE TABLE versions (
    id           TEXT    PRIMARY KEY,                      -- ver_...
    account_id   TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    node_id      TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    blob_id      TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    sha256       TEXT    NOT NULL DEFAULT '',
    etag         TEXT    NOT NULL,
    mime_type    TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,                         -- when this content was first written
    created_by   TEXT    NOT NULL DEFAULT '',
    replaced_at  INTEGER NOT NULL,                         -- when it stopped being the current content
    replaced_by  TEXT    NOT NULL DEFAULT '',
    label        TEXT,                                     -- at most 100 characters, or NULL
    keep_forever INTEGER NOT NULL DEFAULT 0 CHECK (keep_forever IN (0, 1))
) STRICT;
CREATE INDEX versions_node ON versions (node_id, created_at);
CREATE INDEX versions_account ON versions (account_id, replaced_at) WHERE keep_forever = 0;
CREATE INDEX versions_blob ON versions (blob_id);
