-- Derived files: thumbnails (design section 14, slice s1, decision 33).
--
-- One row is one derived artefact of one blob. The file itself is under derived/<account>/<id>, and the row
-- says whether it is ready. The key is (blob_id, kind, converter): blobs never change, so a derivative of a
-- blob is never stale, and a new converter version makes new rows, so an old thumbnail is not served as current.
--
-- state is pending while a request has queued the work, ready once the file is in place, and failed when the
-- work did not finish. A failure caused by the image (corrupt, over a cap, unsupported) is sticky for its key:
-- retry_at is NULL. A failure caused by the server or the host (a signal, a time limit, a full disk, a decoder
-- that ran out of memory) is retried after retry_at, up to a fixed number of attempts, then it is sticky too.
-- The converter key holds the limits that decide a refusal, so raising one makes a new key. A failed row is
-- removed by the sweep 30 days after failed_at.
--
-- blob_id cascades from blobs, so a removed blob takes its rows with it. The sweep removes the files first
-- (internal/derived), because a cascade deletes rows and not files. last_used_at drives the budget's
-- least-recently-used eviction.

CREATE TABLE derived (
	id           TEXT PRIMARY KEY,
	account_id   TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
	blob_id      TEXT NOT NULL REFERENCES blobs (id) ON DELETE CASCADE,
	kind         TEXT NOT NULL CHECK (length(kind) BETWEEN 1 AND 32),
	converter    TEXT NOT NULL CHECK (length(converter) BETWEEN 1 AND 64),
	state        TEXT NOT NULL CHECK (state IN ('pending', 'ready', 'failed')),
	size         INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
	sha256       TEXT NOT NULL DEFAULT '' CHECK (length(sha256) IN (0, 64)),
	error        TEXT NOT NULL DEFAULT '' CHECK (length(error) <= 200),
	created_at   INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL,
	failed_at    INTEGER,
	attempts     INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 100),
	retry_at     INTEGER,
	UNIQUE (blob_id, kind, converter)
);

-- The budget's eviction reads the least recently used ready rows; the sum of ready sizes reads the same rows.
CREATE INDEX derived_lru ON derived (state, last_used_at);
CREATE INDEX derived_account ON derived (account_id);
CREATE INDEX derived_failed ON derived (failed_at) WHERE state = 'failed';
