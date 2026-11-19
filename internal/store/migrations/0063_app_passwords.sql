-- App passwords: a person's credential for the read-only WebDAV mount (decision 236).
--
-- A row is one app password of one user: a label, an access level (read or write) and the hash of a secret shown once
-- when it is made. The secret itself is never stored, as an API key's is not (api_keys, migration 0002): only its
-- SHA-256, looked up on each mount request. A row is ended by its revoked_at; the cascades remove it with the user and
-- with the account. created_at, revoked_at and last_used_at are Unix milliseconds. A user keeps at most 20 unrevoked
-- app passwords, counted in the write that adds one.

CREATE TABLE app_passwords (
    id           TEXT    PRIMARY KEY,
    account_id   TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    access       TEXT    NOT NULL, -- 'read' or 'write'
    prefix       TEXT    NOT NULL, -- the start of the secret, safe to display
    secret_hash  BLOB    NOT NULL UNIQUE, -- SHA-256 of the whole secret
    created_at   INTEGER NOT NULL,
    revoked_at   INTEGER,
    last_used_at INTEGER
) STRICT;

CREATE INDEX app_passwords_user ON app_passwords (account_id, user_id);