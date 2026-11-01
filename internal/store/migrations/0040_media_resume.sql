-- Where a user stopped a video or audio file (design section 14, slice x2, decision 52).
--
-- A row is one user's place in one file. It is private to its user: no route reads another user's row, and the
-- cascades remove it with the user, with the file (the node) and with the account. Times are Unix milliseconds for
-- updated_at; the position and the duration are milliseconds of media time.
--
-- The client writes a row at most every 15 seconds while the file plays, and on pause. A file that ends is written as
-- position 0, so the row stays small and the next play starts at the beginning.

CREATE TABLE media_resume (
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id     TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL CHECK (position_ms >= 0),
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, node_id)
) STRICT;
