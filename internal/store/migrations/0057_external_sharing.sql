-- External sharing (design section 8, decision 187): a grant of a node to an email address of another
-- account, and the proof that an address belongs to the user who holds it. Nothing here opens a read
-- path across accounts; the reach is a later slice's. Both switches are off by default.
--
-- The account's switch gates new grants and the recipient's list. Turning it off keeps the grants.
ALTER TABLE accounts ADD COLUMN external_sharing INTEGER NOT NULL DEFAULT 0 CHECK (external_sharing IN (0, 1));

-- The proof follows the address: a user is proved while email_proved_key is the key of the address in
-- use. A change of address leaves the user unproved until the new one is proved.
ALTER TABLE users ADD COLUMN email_proved_key TEXT;
ALTER TABLE users ADD COLUMN email_proved_at INTEGER;

-- A verification remembers the address its token was mailed to. A token returned to the caller or made
-- by a command has none, and proves nothing when it is used.
ALTER TABLE verifications ADD COLUMN mailed_to TEXT;

-- A proof token is a single-use token of its own, mailed to the address in use. Only the hash is stored.
CREATE TABLE email_proofs (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email_key  TEXT    NOT NULL,
    token_hash BLOB    NOT NULL UNIQUE, -- SHA-256 of the token; the token is only ever mailed
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,                 -- set once; a used token is refused
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX email_proofs_user ON email_proofs (user_id);

-- A grant names an address, not a user: the address may hold no user anywhere, and the answer is the
-- same either way. The role is viewer only. The account is the sharer's, the node's own.
CREATE TABLE external_permissions (
    node_id    TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    email      TEXT    NOT NULL,
    email_key  TEXT    NOT NULL,
    role       TEXT    NOT NULL CHECK (role = 'viewer'),
    created_by TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER,
    PRIMARY KEY (node_id, email_key)
) STRICT;

CREATE INDEX external_permissions_email ON external_permissions (email_key);
