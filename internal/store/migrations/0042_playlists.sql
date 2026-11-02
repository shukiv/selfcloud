-- Saved playlists (design section 14, slice x4, decision 58).
--
-- A playlist is one user's named, ordered list of files. It is private to its user: no route reads another user's
-- playlist, and the audit entries about it are private to the user too (PrivateUserID).
--
-- An entry is a reference to a node, and nothing more. node_id has NO foreign key on purpose: an entry whose node was
-- purged stays in the list and shows as unavailable, as an entry whose access was removed does. Reach is decided at
-- each read (decision 58), so the row says nothing about whether the file still exists. The entries of a playlist go
-- with the playlist; the playlists go with their user and with their account.

CREATE TABLE playlists (
    id         TEXT    PRIMARY KEY,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX playlists_user ON playlists (user_id, created_at, id);
-- Account deletion finds the account's playlists by this column (the cascade does, and the index keeps it from a scan).
CREATE INDEX playlists_account ON playlists (account_id);

CREATE TABLE playlist_entries (
    id          TEXT    PRIMARY KEY,
    playlist_id TEXT    NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    position    INTEGER NOT NULL CHECK (position >= 0),
    node_id     TEXT    NOT NULL,
    added_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX playlist_entries_order ON playlist_entries (playlist_id, position);
