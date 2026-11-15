-- Embedding Selfcloud in a host application's page (decision 185). account_embed_origins holds the host sites an
-- account allows to frame its pages; the installation's own list is the config's embed.origins, and the two are unioned.
-- A sign-in link and a session carry frame = 1 when they were made for a frame: the link's URL names the account in a
-- query before the fragment, and the session's cookie is partitioned so it works in the host's third-party context.
-- Existing rows are tab links and sessions, as they were. Decision 120 keeps this additive: a new table and two columns.

CREATE TABLE account_embed_origins (
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    origin     TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (account_id, origin)
) STRICT;

ALTER TABLE sign_in_links ADD COLUMN frame INTEGER NOT NULL DEFAULT 0 CHECK (frame IN (0, 1));
ALTER TABLE sessions ADD COLUMN frame INTEGER NOT NULL DEFAULT 0 CHECK (frame IN (0, 1));