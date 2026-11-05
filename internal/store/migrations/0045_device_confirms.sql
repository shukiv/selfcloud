-- Device confirmations: a single-use link that confirms a new browser once the email's sign-in cap is spent (sec2,
-- decision 86). Times are Unix milliseconds. token_hash and nonce_hash are SHA-256 hashes: the token is only in the mail,
-- and the nonce only in the cookie of the browser that asked. short is the "Keep me signed in" choice the attempt made.
--
-- A separate table, not verifications: that table's purpose is limited to verify and reset (0013 gives the same reason).

CREATE TABLE device_confirms (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE,
    nonce_hash BLOB    NOT NULL,
    short      INTEGER NOT NULL DEFAULT 0 CHECK (short IN (0, 1)),
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX device_confirms_user ON device_confirms (user_id);
CREATE INDEX device_confirms_expires ON device_confirms (expires_at);
