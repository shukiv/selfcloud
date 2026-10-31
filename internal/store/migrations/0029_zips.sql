-- Stored zip downloads (design section 14, slice s2, decision 35).
--
-- A zip is made by a job of kind node.zip whose target is the zip's ID. Its file is under
-- derived/zips/<account>/<id>.zip, written as <id>.part until it is ready. Only the caller who asked
-- for it (created_by and created_kind) may download or delete it.
--
-- state is pending while the job makes the file, ready once the file is in place, failed otherwise.
-- reserved is the snapshot's byte total while the job runs: it counts against zip.max_disk, so
-- unfinished zips cannot overrun the disk. It is 0 once the zip is ready or failed.
-- expires_at is snapshot time plus zip.ttl; the sweep removes the row, then the file, at that time.
-- snapshot_at is the time the tree was read; the file is a snapshot of that time.
--
-- zip_roots holds the nodes the caller asked for, in order. Every download re-checks access to each of
-- them. node_id is not a foreign key, so a purged root leaves a row that refuses the download.

CREATE TABLE zips (
	id           TEXT PRIMARY KEY,
	account_id   TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
	created_by   TEXT NOT NULL CHECK (length(created_by) BETWEEN 1 AND 64),
	created_kind TEXT NOT NULL CHECK (length(created_kind) BETWEEN 1 AND 32),
	-- The maker's credential: the fingerprint of the delegated token it came in on, or '' for any other
	-- credential (a session, a key). Only the same credential reads or deletes the zip.
	created_cred TEXT NOT NULL DEFAULT '' CHECK (length(created_cred) <= 64),
	state        TEXT NOT NULL CHECK (state IN ('pending', 'ready', 'failed')),
	size         INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
	sha256       TEXT NOT NULL DEFAULT '' CHECK (length(sha256) IN (0, 64)),
	entries      INTEGER NOT NULL DEFAULT 0 CHECK (entries >= 0),
	skipped      INTEGER NOT NULL DEFAULT 0 CHECK (skipped >= 0),
	reserved     INTEGER NOT NULL DEFAULT 0 CHECK (reserved >= 0),
	snapshot_at  INTEGER,
	expires_at   INTEGER,
	error        TEXT NOT NULL DEFAULT '' CHECK (length(error) <= 200),
	created_at   INTEGER NOT NULL
);

CREATE TABLE zip_roots (
	zip_id  TEXT NOT NULL REFERENCES zips (id) ON DELETE CASCADE,
	seq     INTEGER NOT NULL CHECK (seq >= 0),
	node_id TEXT NOT NULL CHECK (length(node_id) BETWEEN 1 AND 64),
	PRIMARY KEY (zip_id, seq)
);

-- The sweep reads ready rows by expiry; the limits count rows by account and by caller.
CREATE INDEX zips_state_expiry ON zips (state, expires_at);
CREATE INDEX zips_account_state ON zips (account_id, state);
CREATE INDEX zips_caller_state ON zips (account_id, created_by, state);
