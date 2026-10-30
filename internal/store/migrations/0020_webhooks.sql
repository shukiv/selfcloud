-- Webhooks: endpoints, their event lists, the outbox and its deliveries, and the quota marks (design
-- section 10, slice j). Times are Unix milliseconds. Slice j1 uses only webhook_endpoints and
-- webhook_endpoint_events; the other three tables are created now so that the later parts add no schema.
--
-- The secret is stored encrypted (secret_enc, previous_secret_enc): nonce then ciphertext, sealed with the
-- account, endpoint and column as additional data (internal/webhook). The plain secret is never stored.
--
-- Every row carries account_id, and the join tables reference their parent with the account too, so an
-- event row can never name an endpoint of another account.

CREATE TABLE webhook_endpoints (
    id                   TEXT    PRIMARY KEY,
    account_id           TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    url                  TEXT    NOT NULL CHECK (length(url) BETWEEN 1 AND 2048),
    description          TEXT    CHECK (description IS NULL OR length(description) <= 200),
    state                TEXT    NOT NULL CHECK (state IN ('active', 'paused', 'disabled')),
    disabled_reason      TEXT    CHECK (disabled_reason IS NULL OR disabled_reason IN ('failing', 'overflow', 'secret_unreadable')),
    secret_enc           BLOB    NOT NULL CHECK (length(secret_enc) > 0),
    previous_secret_enc  BLOB,
    previous_secret_until INTEGER,
    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    last_success_at      INTEGER,
    last_failure_at      INTEGER,
    created_by           TEXT    NOT NULL,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    -- A reason is set exactly when the endpoint is disabled.
    CHECK ((state = 'disabled') = (disabled_reason IS NOT NULL)),
    -- The grace secret and its end time come together.
    CHECK ((previous_secret_enc IS NULL) = (previous_secret_until IS NULL)),
    UNIQUE (id, account_id)
) STRICT;

CREATE INDEX webhook_endpoints_account ON webhook_endpoints (account_id, created_at, id);

CREATE TABLE webhook_endpoint_events (
    endpoint_id TEXT NOT NULL,
    account_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    event       TEXT NOT NULL CHECK (event IN ('user.created', 'user.verified', 'file.created', 'file.updated',
                    'file.deleted', 'file.moved', 'upload.completed', 'quota.warning', 'quota.exceeded',
                    'webhook.test')),
    PRIMARY KEY (endpoint_id, event),
    FOREIGN KEY (endpoint_id, account_id) REFERENCES webhook_endpoints (id, account_id) ON DELETE CASCADE
) STRICT;

CREATE INDEX webhook_endpoint_events_account ON webhook_endpoint_events (account_id);

-- The outbox. An event is written in the transaction of the change it reports (slice j2).
CREATE TABLE webhook_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id   TEXT    NOT NULL UNIQUE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    type       TEXT    NOT NULL CHECK (type IN ('user.created', 'user.verified', 'file.created', 'file.updated',
                   'file.deleted', 'file.moved', 'upload.completed', 'quota.warning', 'quota.exceeded',
                   'webhook.test')),
    payload    TEXT    NOT NULL CHECK (length(payload) <= 16384),
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX webhook_events_account ON webhook_events (account_id, id);

-- One delivery per endpoint per event. last_error never holds the URL or the body.
CREATE TABLE webhook_deliveries (
    id              TEXT    PRIMARY KEY,
    account_id      TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    endpoint_id     TEXT    NOT NULL,
    event_row_id    INTEGER NOT NULL REFERENCES webhook_events (id) ON DELETE CASCADE,
    state           TEXT    NOT NULL CHECK (state IN ('pending', 'delivered', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at INTEGER NOT NULL,
    lease_until     INTEGER,
    last_code       INTEGER,
    last_error      TEXT    CHECK (last_error IS NULL OR length(last_error) <= 200),
    created_at      INTEGER NOT NULL,
    finished_at     INTEGER,
    UNIQUE (endpoint_id, event_row_id),
    FOREIGN KEY (endpoint_id, account_id) REFERENCES webhook_endpoints (id, account_id) ON DELETE CASCADE
) STRICT;

CREATE INDEX webhook_deliveries_due ON webhook_deliveries (state, next_attempt_at);
CREATE INDEX webhook_deliveries_log ON webhook_deliveries (endpoint_id, created_at);

-- A row says the account or user is already past a quota level, so each crossing is announced once.
CREATE TABLE webhook_quota_marks (
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    scope      TEXT    NOT NULL CHECK (scope IN ('account', 'user')),
    scope_id   TEXT    NOT NULL,
    level      TEXT    NOT NULL CHECK (level IN ('warning', 'exceeded')),
    set_at     INTEGER NOT NULL,
    PRIMARY KEY (account_id, scope, scope_id, level)
) STRICT;
