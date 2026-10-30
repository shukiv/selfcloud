-- Stars and recent items (slice p2). Times are Unix milliseconds.
--
-- A star is a user's bookmark on a node. It is private to its user: no route reads another user's stars.
-- A star on a node the user can no longer reach stays here and is not listed. A purged node or a deleted
-- user removes its stars through the cascades.
--
-- A recent entry records a user's last view (a session user opening a file) and its last edit (any user
-- who writes the file). last_at is the later of the two and orders the list. The sweep prunes entries older
-- than 30 days, and keeps at most 500 per user. Views reach the table through the recorder, in batches.

CREATE TABLE stars (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id    TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, node_id)
) STRICT;

CREATE INDEX stars_user ON stars (user_id, created_at DESC);

CREATE TABLE recent (
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id    TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    viewed_at  INTEGER,
    edited_at  INTEGER,
    last_at    INTEGER NOT NULL,
    PRIMARY KEY (user_id, node_id),
    CHECK (viewed_at IS NOT NULL OR edited_at IS NOT NULL)
) STRICT;

CREATE INDEX recent_user ON recent (user_id, last_at DESC);
