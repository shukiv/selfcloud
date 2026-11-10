-- Pending device sign-in requests (decision 125). The terminal holds the device code; the browser holds the user code, which
-- is shown only in the terminal. Only hashes are stored: the device code's, the code's first half's (the selector, which
-- finds the row) and the whole user code's (which is compared in constant time). A request dies at expires_at, which is
-- 600 s after it began and never moves. attempts counts wrong codes; a request with five is deleted. The approval names
-- the approver's account and user, and the poll that claims it deletes the row as it makes the device.

CREATE TABLE cli_device_requests (
    id               TEXT    PRIMARY KEY,
    device_code_hash BLOB    NOT NULL UNIQUE,
    selector_hash    BLOB    NOT NULL UNIQUE,
    code_hash        BLOB    NOT NULL,
    client_name      TEXT    NOT NULL,
    client_ip        TEXT    NOT NULL,
    user_agent       TEXT    NOT NULL,
    created_at       INTEGER NOT NULL,
    expires_at       INTEGER NOT NULL,
    interval_ms      INTEGER NOT NULL,
    last_poll_at     INTEGER NOT NULL,
    attempts         INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    approved_at      INTEGER,
    account_id       TEXT    REFERENCES accounts (id) ON DELETE CASCADE,
    user_id          TEXT    REFERENCES users (id) ON DELETE CASCADE
) STRICT;

CREATE INDEX cli_device_requests_expires ON cli_device_requests (expires_at);
