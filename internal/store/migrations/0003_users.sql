-- Users, their verification and password-reset tokens, and web sessions.
-- Times are Unix milliseconds.

CREATE TABLE users (
    id            TEXT    PRIMARY KEY,
    account_id    TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    external_id   TEXT,
    email         TEXT    NOT NULL, -- as given, trimmed
    email_key     TEXT    NOT NULL, -- lowercased; the sign-in key
    display_name  TEXT    NOT NULL DEFAULT '',
    locale        TEXT    NOT NULL DEFAULT 'en',
    role          TEXT    NOT NULL CHECK (role IN ('owner', 'member')),
    status        TEXT    NOT NULL CHECK (status IN ('pending', 'active', 'disabled')),
    password_hash TEXT,   -- argon2id PHC string; NULL until the user sets a password
    verified_at   INTEGER,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE (account_id, email_key)
) STRICT;

CREATE UNIQUE INDEX users_external_id ON users (account_id, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX users_list ON users (account_id, created_at, id);
-- Sign-in looks a user up by email across accounts.
CREATE INDEX users_email ON users (email_key);

CREATE TABLE verifications (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT    NOT NULL CHECK (purpose IN ('verify', 'reset')),
    token_hash BLOB    NOT NULL UNIQUE, -- SHA-256 of the token; the token is only ever mailed or returned
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,                 -- set once; a used token is refused
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX verifications_user ON verifications (user_id, purpose);

CREATE TABLE sessions (
    id_hash      BLOB    PRIMARY KEY, -- SHA-256 of the cookie value
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    account_id   TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    csrf_token   TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    ip           TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX sessions_user ON sessions (user_id);
