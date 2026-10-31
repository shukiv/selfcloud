-- Search over names (design section 14, slice q, decision 40).
--
-- search_names holds one row per node that is searchable: every named node of a published folder, trashed or not (a trashed
-- node is hidden by the reach check, and its row goes at purge). A folder's root has no name and no row; a staged node
-- has no row. node_id is unique and cascades from nodes; the row's id is the rowid of the two full-text tables, and it is
-- an INTEGER PRIMARY KEY, so it never changes. kind is the node kind; ext is the lower-case extension of a file, or ''.
--
-- search_words (unicode61, word prefixes) and search_trigram (trigram, substrings of three or more characters) are
-- contentful FTS5 tables. Their text is names.SearchForm of the name: no marks, case folded. Their account_id column
-- narrows a query inside the index; the query also checks search_names.account_id itself. A cascade does not reach
-- these tables: the store deletes their rows in the same write as the node, the folder or the account (store
-- search_index.go). A test checks each path.
--
-- search_backlog is work for the search job: the nodes of folder_id whose rowid lies in (cursor, upto] that still need
-- search rows. A bulk write records the rowid range it made (a copy's publish, a larger synchronous copy); a small copy
-- indexes its own nodes in its write and records nothing. cursor is the last rowid the job has read, so a batch starts
-- after it, and an item is removed when its range is done. Items run in ID order. A new item never changes another
-- item's cursor, so frequent copies do not restart each other's work. Backfill for an upgrade is one item per published
-- folder, covering its whole rowid range.
--
-- The nodes table is a rowid table, and the (folder_id) index on nodes stores the rowid after folder_id. So a batch
-- reads "folder_id = ? AND rowid > ? AND rowid <= ?" as one index range, and no index is changed for search.

CREATE TABLE search_names (
    id         INTEGER PRIMARY KEY,
    node_id    TEXT    NOT NULL UNIQUE REFERENCES nodes (id) ON DELETE CASCADE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL CHECK (kind IN ('file', 'dir', 'shortcut')),
    ext        TEXT    NOT NULL DEFAULT '' CHECK (length(ext) <= 16)
) STRICT;

CREATE INDEX search_names_account ON search_names (account_id, id);

CREATE VIRTUAL TABLE search_words USING fts5 (text, account_id, tokenize = 'unicode61 remove_diacritics 2');
CREATE VIRTUAL TABLE search_trigram USING fts5 (text, account_id, tokenize = 'trigram');

CREATE TABLE search_backlog (
    id         INTEGER PRIMARY KEY,
    folder_id  TEXT    NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    cursor     INTEGER NOT NULL,
    upto       INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX search_backlog_folder ON search_backlog (folder_id);

INSERT INTO search_backlog (folder_id, account_id, cursor, upto, created_at)
    SELECT f.id, f.account_id, 0, MAX(n.rowid), 0 FROM folders f JOIN nodes n ON n.folder_id = f.id
    WHERE f.staged = 0 GROUP BY f.id;

-- Label search (design section 4.5): a predicate on one field's value reads these.
CREATE INDEX node_labels_text ON node_labels (field_id, value_text) WHERE value_text IS NOT NULL;
CREATE INDEX node_labels_num ON node_labels (field_id, value_num) WHERE value_num IS NOT NULL;
CREATE INDEX node_labels_date ON node_labels (field_id, value_date) WHERE value_date IS NOT NULL;
CREATE INDEX node_labels_choice ON node_labels (field_id, value_choice) WHERE value_choice IS NOT NULL;
CREATE INDEX node_labels_user_field ON node_labels (field_id, value_user) WHERE value_user IS NOT NULL;
