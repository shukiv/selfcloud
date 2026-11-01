-- Search over file content (design section 14, slice q3, decision 46).
--
-- search_texts has one row per file blob that content search has to look at. The row is the work item as well as the
-- result: state 'pending' is queued work (the content worker takes it), 'ready' has its text in search_text_fts, 'skipped'
-- is a permanent answer (too large, not text, office disabled), and 'failed' is a sticky refusal by the converter that is
-- tried again when next_try has passed. A transient converter outage keeps 'pending' with a later next_try, and counts
-- nothing. The row is keyed by blob, so copies of a file share one text; a blob with no node and no version has no row,
-- and a row goes when its blob goes (store DeleteOrphan), with its full-text row first.
--
-- search_text_fts is contentful. Its text is the search form of the extracted text (names.SearchForm: marks removed, case
-- folded), cut to search.text_max_bytes before folding. Its rowid is search_texts.id. The store deletes the full-text rows
-- itself, because a cascade does not reach them. Words only: a content term matches the start of a word (decision 46).
--
-- search_text_backfill is the one-row cursor of the upgrade backfill: the worker adds rows for the file blobs of the nodes
-- in batches, in blob-ID order after `after`, and sets done when the last batch is short. A blob that gets its node later
-- is queued in the write that gives it the node, so the backfill never has to look again. The migration writes no rows
-- itself: a backfill in one migration transaction would hold the single writer for as long as the instance has files.

CREATE TABLE search_texts (
    id         INTEGER PRIMARY KEY,
    blob_id    TEXT    NOT NULL UNIQUE REFERENCES blobs (id) ON DELETE CASCADE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    state      TEXT    NOT NULL CHECK (state IN ('pending', 'ready', 'failed', 'skipped')),
    source     TEXT    NOT NULL DEFAULT '' CHECK (source IN ('', 'plain', 'converter')),
    converter  TEXT    NOT NULL DEFAULT '' CHECK (length(converter) <= 64),
    bytes      INTEGER NOT NULL DEFAULT 0 CHECK (bytes >= 0),
    truncated  INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1)),
    error      TEXT    NOT NULL DEFAULT '' CHECK (length(error) <= 200),
    attempts   INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_try   INTEGER NOT NULL DEFAULT 0
) STRICT;

-- The worker's claim: the first due pending row of the next account in key order (round robin across accounts).
CREATE INDEX search_texts_claim ON search_texts (state, account_id, id);
-- The retry step: failed rows whose wait has passed.
CREATE INDEX search_texts_retry ON search_texts (state, next_try);
CREATE INDEX search_texts_account ON search_texts (account_id);

CREATE VIRTUAL TABLE search_text_fts USING fts5 (text, account_id, tokenize = 'unicode61 remove_diacritics 2');

CREATE TABLE search_text_backfill (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    after TEXT    NOT NULL DEFAULT '',
    done  INTEGER NOT NULL DEFAULT 0 CHECK (done IN (0, 1))
) STRICT;

INSERT INTO search_text_backfill (id, after, done) VALUES (1, '', 0);

-- search_text_usage is the stored text of each account, in bytes of extracted text (the ready rows' bytes). It is kept
-- in the write that makes a row ready or drops one, so the caps of search.max_text_per_account and search.max_text_total
-- are read in one row, not summed over the rows (decision 46). It is not charged to the tenant's quota.
CREATE TABLE search_text_usage (
    account_id TEXT    PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    bytes      INTEGER NOT NULL DEFAULT 0 CHECK (bytes >= 0)
) STRICT;
