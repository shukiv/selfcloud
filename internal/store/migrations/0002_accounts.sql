-- Accounts (tenants), API keys, the audit log and background jobs.
-- Times are Unix milliseconds.

CREATE TABLE accounts (
    id                TEXT    PRIMARY KEY,
    external_id       TEXT,
    name              TEXT    NOT NULL,
    status            TEXT    NOT NULL CHECK (status IN ('active', 'suspended', 'deleting')),
    quota_bytes       INTEGER NOT NULL DEFAULT 0 CHECK (quota_bytes >= 0), -- 0 = unlimited
    used_bytes        INTEGER NOT NULL DEFAULT 0,
    used_files        INTEGER NOT NULL DEFAULT 0,
    reserved_bytes    INTEGER NOT NULL DEFAULT 0, -- held by uploads in progress
    tokens_not_before INTEGER NOT NULL DEFAULT 0, -- delegated tokens issued earlier are void
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX accounts_external_id ON accounts (external_id) WHERE external_id IS NOT NULL;
CREATE INDEX accounts_created ON accounts (created_at, id);

CREATE TABLE api_keys (
    id           TEXT    PRIMARY KEY,
    account_id   TEXT    REFERENCES accounts (id) ON DELETE CASCADE, -- NULL: installation admin key
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL, -- the start of the key, safe to display
    hash         BLOB    NOT NULL UNIQUE, -- SHA-256 of the whole key
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER,
    revoked_at   INTEGER,
    last_used_at INTEGER
) STRICT;

CREATE INDEX api_keys_account ON api_keys (account_id);

-- The audit log outlives what it describes, so account_id is not a foreign key.
CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY, -- increases with time; the paging cursor
    time       INTEGER NOT NULL,
    account_id TEXT,
    actor_kind TEXT    NOT NULL,
    actor_id   TEXT    NOT NULL,
    action     TEXT    NOT NULL,
    target     TEXT    NOT NULL DEFAULT '',
    ip         TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    outcome    TEXT    NOT NULL DEFAULT 'ok',
    details    TEXT -- JSON object
) STRICT;

CREATE INDEX audit_log_account ON audit_log (account_id, id);

-- Background jobs. account_id is not a foreign key: a job that deletes an account outlives it.
CREATE TABLE jobs (
    id          TEXT    PRIMARY KEY,
    account_id  TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    target      TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    error       TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    finished_at INTEGER
) STRICT;

CREATE INDEX jobs_queue ON jobs (status, created_at, id);
CREATE INDEX jobs_account ON jobs (account_id, kind, status);
