-- Where a user marked places in a video or audio file (design section 14, slice mp6a, decision 216).
--
-- A row is one user's bookmark in one file: a time and an optional label. It is private to its user, as the place a
-- user stopped at is (media_resume, migration 0040): no route reads another user's row, and the cascades remove it
-- with the user, with the file (the node) and with the account. created_at is Unix milliseconds; position_ms is
-- milliseconds of media time. A user keeps at most 200 bookmarks in a file, counted in the write that adds one.

CREATE TABLE media_bookmarks (
    id          TEXT    PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id     TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL CHECK (position_ms >= 0),
    label       TEXT    NOT NULL,
    created_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX media_bookmarks_user ON media_bookmarks (user_id, node_id, position_ms);
-- File deletion finds the file's bookmarks by this column (the cascade does, and the index keeps it from a scan).
CREATE INDEX media_bookmarks_node ON media_bookmarks (node_id);
