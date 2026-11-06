-- A user's email can be changed through the API (design section 8, slice ue, decision 89). The new address waits in
-- users.pending_email until its confirmation link is used, and that link is a verification of purpose email_change. The
-- old address stays in use until then. SQLite cannot change a CHECK, so verifications is rebuilt with the new purpose; its
-- rows, its index and its token hashes are kept. Nothing refers to verifications, so foreign keys need not be off.

ALTER TABLE users ADD COLUMN pending_email TEXT;

CREATE TABLE verifications_new (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT    NOT NULL CHECK (purpose IN ('verify', 'reset', 'email_change')),
    token_hash BLOB    NOT NULL UNIQUE, -- SHA-256 of the token; the token is only ever mailed or returned
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,                 -- set once; a used token is refused
    created_at INTEGER NOT NULL
) STRICT;

INSERT INTO verifications_new (id, user_id, purpose, token_hash, expires_at, used_at, created_at)
    SELECT id, user_id, purpose, token_hash, expires_at, used_at, created_at FROM verifications;

DROP TABLE verifications;

ALTER TABLE verifications_new RENAME TO verifications;

CREATE INDEX verifications_user ON verifications (user_id, purpose);
