-- Office editing through WOPI (design section 10, slice l, decision 31).
--
-- wopi_locks holds one lock per file while an editor holds it. The lock is not enforced until slice l3 (the lock
-- check in the store); slice l1 and l2 only create the table. A lock is kept with its file: a purge removes it.
--
-- wopi_sessions holds one row per editing session a POST .../office answer opened. Its token names the row, so a
-- revoked row stops the token at once, before the token's own expiry. user_id is NULL when an account key opened
-- the editor, because such a session acts for the key and no user. browser_session is the hash of the browser
-- session that opened the editor, and it is NULL when a delegated token opened it. Every WOPI call checks that the
-- browser session still exists, so signing out, a password change, a reset or a disabled user ends the editor too.
--
-- Times are Unix milliseconds, as the other tables keep them.

CREATE TABLE wopi_locks (
    node_id     TEXT    PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    lock_id     TEXT    NOT NULL CHECK (length(lock_id) BETWEEN 1 AND 1024),
    holder      TEXT    NOT NULL CHECK (length(holder) BETWEEN 1 AND 64),
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL
);

CREATE INDEX wopi_locks_expires ON wopi_locks (expires_at);

CREATE TABLE wopi_sessions (
    id          TEXT    PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 64),
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    node_id     TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    user_id     TEXT    REFERENCES users (id) ON DELETE CASCADE,
    browser_session BLOB,
    role        TEXT    NOT NULL CHECK (role IN ('read', 'write')),
    issued_at   INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL CHECK (expires_at > issued_at),
    revoked_at  INTEGER
);

CREATE INDEX wopi_sessions_expires ON wopi_sessions (expires_at);
CREATE INDEX wopi_sessions_user ON wopi_sessions (account_id, user_id);
