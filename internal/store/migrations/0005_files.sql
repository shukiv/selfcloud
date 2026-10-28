-- File contents: the blobs table. The nodes table (the file tree) was created by 0004.
-- Times are Unix milliseconds.
--
-- A blob is one immutable file on disk, named by its ID. Its state is:
--   uploading  a file being written; not referenced yet (reserved for resumable uploads)
--   live       committed and referenced by at least one node, or waiting to be referenced
--   orphan     no node refers to it; the file is removed once delete_after has passed
-- A blob row is written in the same transaction as the node that first refers to it, so a live
-- blob with no reference only exists after the last reference has gone.

CREATE TABLE blobs (
    id           TEXT    PRIMARY KEY,           -- blb_..., also the file name on disk
    account_id   TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    size         INTEGER NOT NULL CHECK (size >= 0),
    sha256       TEXT    NOT NULL,              -- hex
    state        TEXT    NOT NULL CHECK (state IN ('uploading', 'live', 'orphan')),
    delete_after INTEGER,                       -- set for orphans: earliest time the file may go
    created_at   INTEGER NOT NULL,
    CHECK ((state = 'orphan') = (delete_after IS NOT NULL))
) STRICT;

CREATE INDEX blobs_account ON blobs (account_id);
-- The collector scans for live blobs to orphan and for orphans that are due.
CREATE INDEX blobs_state ON blobs (state, delete_after);
