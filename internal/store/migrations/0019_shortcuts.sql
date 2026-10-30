-- runner: foreign_keys=off
-- Shortcuts, folder colours and item descriptions (slice p3). Times are Unix milliseconds.
--
-- A shortcut is a node of its own kind, 'shortcut', with size 0. Its target_id names the file or directory it
-- points to. A target that is purged sets target_id to NULL, and the shortcut stays as an unavailable one. A
-- shortcut grants nothing: the access rules never read target_id.
--
-- color is one of the palette names (store.Colors) and belongs to a directory. description is plain text, at
-- most 1000 characters, and belongs to any node.
--
-- The nodes table is rebuilt, which SQLite cannot do with an ALTER: the kind CHECK must change. The runner
-- switches foreign keys off for this migration (the directive above), so that DROP TABLE does not cascade into
-- permissions, uploads, versions, share links, access requests, stars and recent. Every row, index and foreign
-- key is kept. The runner runs PRAGMA foreign_key_check before the commit and rolls back on any violation.
-- Operators: the rebuild takes time in proportion to the number of nodes. Take a backup of the database file
-- before upgrading (the server does not make one).

CREATE TABLE nodes_new (
    id          TEXT    PRIMARY KEY,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    folder_id   TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    parent_id   TEXT    REFERENCES nodes (id) ON DELETE CASCADE, -- NULL only for a folder's root
    name        TEXT    NOT NULL,
    name_key    TEXT    NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('dir', 'file', 'shortcut')),
    size        INTEGER NOT NULL DEFAULT 0,
    mime_type   TEXT    NOT NULL DEFAULT '',
    blob_id     TEXT,
    etag        TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    modified_at INTEGER NOT NULL,
    created_by  TEXT    NOT NULL DEFAULT '',
    modified_by TEXT    NOT NULL DEFAULT '',
    owner_id    TEXT    NOT NULL DEFAULT '',
    content_tag TEXT    NOT NULL DEFAULT '',
    no_download INTEGER NOT NULL DEFAULT 0 CHECK (no_download IN (0, 1)),
    editors_can_share INTEGER NOT NULL DEFAULT 0 CHECK (editors_can_share IN (0, 1)),
    trashed_at  INTEGER,
    trashed_by  TEXT    NOT NULL DEFAULT '',
    trashed_name_key TEXT NOT NULL DEFAULT '',
    color       TEXT    CHECK (color IS NULL OR color IN ('red', 'orange', 'yellow', 'green', 'teal', 'blue',
                        'indigo', 'purple', 'pink', 'brown', 'slate', 'grey')),
    description TEXT    CHECK (description IS NULL OR length(description) <= 1000),
    target_id   TEXT    REFERENCES nodes (id) ON DELETE SET NULL,
    CHECK (target_id IS NULL OR kind = 'shortcut'),
    CHECK (kind <> 'shortcut' OR size = 0),
    CHECK (color IS NULL OR kind = 'dir')
) STRICT;

INSERT INTO nodes_new (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, blob_id, etag,
    created_at, modified_at, created_by, modified_by, owner_id, content_tag, no_download, editors_can_share,
    trashed_at, trashed_by, trashed_name_key)
    SELECT id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, blob_id, etag,
    created_at, modified_at, created_by, modified_by, owner_id, content_tag, no_download, editors_can_share,
    trashed_at, trashed_by, trashed_name_key FROM nodes;

DROP TABLE nodes;
ALTER TABLE nodes_new RENAME TO nodes;

CREATE UNIQUE INDEX nodes_child ON nodes (parent_id, name_key);
CREATE UNIQUE INDEX nodes_root ON nodes (folder_id) WHERE parent_id IS NULL;
CREATE INDEX nodes_folder ON nodes (folder_id);
CREATE INDEX nodes_blob ON nodes (blob_id) WHERE blob_id IS NOT NULL;
CREATE INDEX nodes_size ON nodes (folder_id, size DESC, id) WHERE kind = 'file';
CREATE INDEX nodes_trash ON nodes (account_id, trashed_at) WHERE trashed_at IS NOT NULL;
-- A purged target clears its shortcuts through this index, not a scan of the table.
CREATE INDEX nodes_target ON nodes (target_id) WHERE target_id IS NOT NULL;

-- A request that came through a shortcut is filed on the target. via_node_id names the shortcut while it exists;
-- deleting the shortcut clears it. via_shortcut stays 1 for such a request, so a decision mail can tell "the shortcut
-- is gone" from "never through a shortcut". Neither column is shown to anyone: the requester sees only the mail,
-- and the managers see the request on the target as any other.
-- access_requests is not rebuilt: adding a nullable column with a foreign key whose default is NULL is allowed.
ALTER TABLE access_requests ADD COLUMN via_node_id TEXT REFERENCES nodes (id) ON DELETE SET NULL;
ALTER TABLE access_requests ADD COLUMN via_shortcut INTEGER NOT NULL DEFAULT 0 CHECK (via_shortcut IN (0, 1));
CREATE INDEX access_requests_via ON access_requests (via_node_id) WHERE via_node_id IS NOT NULL;
