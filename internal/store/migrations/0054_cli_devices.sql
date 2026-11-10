-- Device sign-ins (decision 125). A row is a device a person approved at a terminal, and its scd_ token names the row. The
-- token itself is never stored: it is signed from the row's fields when the poll claims the approval. Times are Unix
-- milliseconds. A row is ended by its revoked_at, by its expiry, or by a cut-off of its user or account, which the API
-- reads when it checks the token; the sweep removes it once it has expired.

CREATE TABLE cli_devices (
    id          TEXT    PRIMARY KEY,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_name TEXT    NOT NULL,
    client_ip   TEXT    NOT NULL DEFAULT '',
    user_agent  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    revoked_at  INTEGER
) STRICT;

CREATE INDEX cli_devices_user ON cli_devices (user_id);
CREATE INDEX cli_devices_expires ON cli_devices (expires_at);
