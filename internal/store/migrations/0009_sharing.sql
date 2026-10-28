-- Sharing model: roles on any node, inherited downward, with groups and personal folders.
-- Times are Unix milliseconds.
--
-- Folders gain a kind. A team space (kind 'team', the default and every folder made before this
-- migration) is shared by permissions. A personal folder ('personal') belongs to one user, who
-- holds manager on its root as a rule of the access code, not as a row here. Personal folders carry
-- a reserved name key that names.Clean never produces (it refuses "/"), so they cannot collide with
-- a team space's name. The Go backfill in Open makes each existing user's personal folder, since the
-- migration runner is SQL only.
--
-- The folders table is not rebuilt: foreign keys are on, and dropping it would cascade into nodes
-- and uploads.

ALTER TABLE folders ADD COLUMN kind TEXT NOT NULL DEFAULT 'team' CHECK (kind IN ('team', 'personal'));
ALTER TABLE folders ADD COLUMN owner_user_id TEXT REFERENCES users (id);
-- No ON DELETE action: deleting a user first deletes the personal folder through the store, so its
-- usage and reservations are adjusted. The foreign key refuses a delete that skips that step.
CREATE UNIQUE INDEX folders_personal_owner ON folders (owner_user_id) WHERE kind = 'personal';

-- The user who owns a node. A new node's owner is its creator; inside a personal folder it is the
-- folder's user. Ownership transfer (slice n2) changes it.
ALTER TABLE nodes ADD COLUMN owner_id TEXT NOT NULL DEFAULT '';
UPDATE nodes SET owner_id = created_by;

CREATE TABLE groups (
    id          TEXT    PRIMARY KEY,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    external_id TEXT,
    name        TEXT    NOT NULL,
    name_key    TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (account_id, name_key)
) STRICT;
CREATE UNIQUE INDEX groups_external_id ON groups (account_id, external_id) WHERE external_id IS NOT NULL;

CREATE TABLE group_members (
    group_id   TEXT    NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (group_id, user_id)
) STRICT;
CREATE INDEX group_members_user ON group_members (user_id, group_id);

-- A permission gives a user or a group a role on one node. It reaches the node's subtree, and the
-- highest role over the node and its ancestors is the one that applies. subject_id is not a foreign
-- key because it names a user or a group; deleting either removes its rows in the same transaction.
-- expires_at is filtered at every read.
CREATE TABLE permissions (
    node_id      TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    account_id   TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    subject_kind TEXT    NOT NULL CHECK (subject_kind IN ('user', 'group')),
    subject_id   TEXT    NOT NULL,
    role         TEXT    NOT NULL CHECK (role IN ('viewer', 'commenter', 'contributor', 'editor', 'manager')),
    created_at   INTEGER NOT NULL,
    created_by   TEXT    NOT NULL DEFAULT '',
    expires_at   INTEGER,
    PRIMARY KEY (node_id, subject_kind, subject_id)
) STRICT;
CREATE INDEX permissions_subject ON permissions (subject_kind, subject_id);
CREATE INDEX permissions_node ON permissions (node_id);

-- Every grant becomes a user permission on its folder's root: read is viewer, write is editor.
INSERT INTO permissions (node_id, account_id, subject_kind, subject_id, role, created_at, created_by)
    SELECT r.id, f.account_id, 'user', g.user_id,
           CASE g.level WHEN 'write' THEN 'editor' ELSE 'viewer' END,
           g.created_at, g.created_by
    FROM grants g
    JOIN folders f ON f.id = g.folder_id
    JOIN nodes r ON r.folder_id = f.id AND r.parent_id IS NULL;

DROP TABLE grants;

-- An audit entry about My files is private to the folder's user: owners and admins never see it. The
-- column names that user, and is empty for everything else.
ALTER TABLE audit_log ADD COLUMN private_user_id TEXT;
