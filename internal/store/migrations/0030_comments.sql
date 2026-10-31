-- Comments and mentions (design section 14, slice r, decision 39), part r1 and r2.
--
-- A comment is on a file or a folder (a node). A reply hangs from a top-level comment: parent_id is the top-level
-- comment, and the code refuses a reply to a reply, because the rule needs a read. A deleted comment keeps its row only
-- while it has replies (a tombstone: deleted_at is set and body is empty), so replies still hang from it. A comment
-- with no replies is removed at delete, and the store removes a tombstone when its last reply goes.
--
-- author_id is cleared when its user is deleted: the text stays and the reader sees a former member (decision 39).
-- resolved_by is cleared the same way. A comment is plain text. The body is never rendered as HTML.
--
-- mentions holds one row per user a comment names. Only users who reach the node at write time get a row. The
-- account_id lets account deletion cascade without a join.
--
-- accounts.comments_on is the account's switch. Off refuses every comment route, reads included, and keeps the rows.

CREATE TABLE comments (
	id          TEXT    PRIMARY KEY,
	account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
	node_id     TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
	parent_id   TEXT             REFERENCES comments (id) ON DELETE CASCADE,
	author_id   TEXT             REFERENCES users (id) ON DELETE SET NULL,
	body        TEXT    NOT NULL DEFAULT '' CHECK (length(body) <= 2000),
	created_at  INTEGER NOT NULL,
	edited_at   INTEGER,
	deleted_at  INTEGER,
	resolved_at INTEGER,
	resolved_by TEXT             REFERENCES users (id) ON DELETE SET NULL,
	-- Only a top-level comment resolves, and a tombstone has no body and is not resolved.
	CHECK (parent_id IS NULL OR resolved_at IS NULL),
	CHECK (deleted_at IS NULL OR (body = '' AND resolved_at IS NULL)),
	CHECK (deleted_at IS NOT NULL OR length(body) >= 1)
);

CREATE INDEX comments_node ON comments (node_id, parent_id, created_at, id);

CREATE TABLE mentions (
	comment_id TEXT NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
	user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	account_id TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
	PRIMARY KEY (comment_id, user_id)
);

CREATE INDEX mentions_user ON mentions (user_id, comment_id);

ALTER TABLE accounts ADD COLUMN comments_on INTEGER NOT NULL DEFAULT 1 CHECK (comments_on IN (0, 1));
