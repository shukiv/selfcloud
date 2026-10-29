-- Share links: a link that gives access to one file or directory, and the visits that record who has
-- opened an organisation link. Times are Unix milliseconds.
--
-- A link is not a permission: it never appears in permissions. An organisation link works through its
-- visits: a signed-in member of the account who opens it gets a row in share_link_visits, and the
-- rank queries then count the link for that member. Deleting or pausing the link ends that at once.
--
-- The secret is stored only as its SHA-256 hash, and is shown once, at creation or reset. A visit made
-- before credentials_at (the last reset) no longer counts, so a reset ends the access of earlier copies.
--
-- The columns for anyone and drop links (password_hash, upload_only) are here so later slices add no
-- columns to this table. Only organisation links are created so far.

CREATE TABLE share_links (
    id             TEXT    PRIMARY KEY,
    account_id     TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    node_id        TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    audience       TEXT    NOT NULL CHECK (audience IN ('organisation', 'anyone')),
    role           TEXT    CHECK (role IS NULL OR role IN ('viewer', 'commenter', 'contributor', 'editor')),
    upload_only    INTEGER NOT NULL DEFAULT 0 CHECK (upload_only IN (0, 1)),
    secret_hash    BLOB    NOT NULL UNIQUE,
    password_hash  TEXT,
    expires_at     INTEGER,
    credentials_at INTEGER NOT NULL,
    created_by     TEXT    NOT NULL,
    created_kind   TEXT    NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_by     TEXT    NOT NULL,
    updated_kind   TEXT    NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (node_id, upload_only),
    CHECK ((upload_only = 1 AND role IS NULL) OR (upload_only = 0 AND role IS NOT NULL))
) STRICT;

CREATE INDEX share_links_account ON share_links (account_id);

CREATE TABLE share_link_visits (
    link_id    TEXT    NOT NULL REFERENCES share_links (id) ON DELETE CASCADE,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    visited_at INTEGER NOT NULL,
    PRIMARY KEY (link_id, user_id)
) STRICT;

CREATE INDEX share_link_visits_user ON share_link_visits (user_id);

-- The account's switch for links that anyone can open. Off by default; slice o2 enforces it.
ALTER TABLE accounts ADD COLUMN public_links INTEGER NOT NULL DEFAULT 0 CHECK (public_links IN (0, 1));
