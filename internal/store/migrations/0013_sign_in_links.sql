-- Sign-in links: a single-use, short-lived way for an account key's application to
-- sign a browser in as one of its users. Times are Unix milliseconds.
--
-- A separate table, not verifications: that table's purpose is limited to verify and reset, and
-- widening its CHECK would mean rebuilding it. next is the path to open after sign-in, stored
-- with the token so the link itself carries nothing but the token.

CREATE TABLE sign_in_links (
    id         TEXT    PRIMARY KEY,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE, -- SHA-256 of the token; the token is only ever in the URL
    next       TEXT    NOT NULL DEFAULT '/files',
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,                 -- set once; a used link is refused
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sign_in_links_user ON sign_in_links (user_id);
CREATE INDEX sign_in_links_expires ON sign_in_links (expires_at);
