-- Sharing controls (slice n2). Times are Unix milliseconds.
--
-- A node may forbid downloads to viewers and commenters, and may let editors share it. Both apply to
-- the node's subtree, so the access code reads them up the ancestry. Copies and moves keep them.
ALTER TABLE nodes ADD COLUMN no_download INTEGER NOT NULL DEFAULT 0 CHECK (no_download IN (0, 1));
ALTER TABLE nodes ADD COLUMN editors_can_share INTEGER NOT NULL DEFAULT 0 CHECK (editors_can_share IN (0, 1));

-- Team-space settings. They sit on the folder and apply to every node in it. A personal folder keeps
-- them all off, since its owner is the only one who manages it.
ALTER TABLE folders ADD COLUMN members_only INTEGER NOT NULL DEFAULT 0 CHECK (members_only IN (0, 1));
ALTER TABLE folders ADD COLUMN sharing_managers_only INTEGER NOT NULL DEFAULT 0 CHECK (sharing_managers_only IN (0, 1));
ALTER TABLE folders ADD COLUMN no_download_for_viewers INTEGER NOT NULL DEFAULT 0 CHECK (no_download_for_viewers IN (0, 1));
-- allow_links is stored now and enforced by the public-link slice (o). It does nothing yet.
ALTER TABLE folders ADD COLUMN allow_links INTEGER NOT NULL DEFAULT 0 CHECK (allow_links IN (0, 1));

-- A request for access to a node. A user asks for a role it lacks; a manager approves or denies it.
-- At most one pending request per node and requester: the partial index enforces it.
CREATE TABLE access_requests (
    id          TEXT    PRIMARY KEY,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    node_id     TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        TEXT    NOT NULL CHECK (role IN ('viewer', 'commenter', 'editor')),
    message     TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'denied')),
    created_at  INTEGER NOT NULL,
    decided_at  INTEGER,
    decided_by  TEXT    NOT NULL DEFAULT ''
) STRICT;
CREATE UNIQUE INDEX access_requests_pending ON access_requests (node_id, user_id) WHERE status = 'pending';
CREATE INDEX access_requests_node ON access_requests (node_id, created_at);
CREATE INDEX access_requests_account ON access_requests (account_id, status, created_at);
CREATE INDEX access_requests_user ON access_requests (user_id);

-- A user may hide a team space from its own list. Hiding changes nothing about access.
CREATE TABLE hidden_folders (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    folder_id  TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, folder_id)
) STRICT;
