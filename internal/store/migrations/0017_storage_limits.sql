-- Storage counters per folder, per-user limits and the largest-files index (slice p1). Times are Unix
-- milliseconds.
--
-- A folder carries its share of the account's counters: the bytes of its files, of their versions and
-- of the trashed items in its tree (used_bytes), its files (used_files), and the reservations of its
-- unfinished uploads (reserved_bytes). A reservation belongs to the folder its upload names, which is the
-- folder of the directory it is aimed at: a move of a directory moves those reservations with it. The sum of
-- the folders' counters always equals the account's counters; `selfcloud admin recount-usage` rebuilds both,
-- and the sweep reports a difference.
--
-- users.quota_bytes: NULL follows the account's default, 0 means no limit, a positive value is the user's
-- own limit on its personal folder. accounts.user_quota_bytes is that default; 0 (the default) means none.

ALTER TABLE folders ADD COLUMN used_bytes INTEGER NOT NULL DEFAULT 0 CHECK (used_bytes >= 0);
ALTER TABLE folders ADD COLUMN used_files INTEGER NOT NULL DEFAULT 0 CHECK (used_files >= 0);
ALTER TABLE folders ADD COLUMN reserved_bytes INTEGER NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0);

ALTER TABLE users ADD COLUMN quota_bytes INTEGER CHECK (quota_bytes IS NULL OR quota_bytes >= 0);
ALTER TABLE accounts ADD COLUMN user_quota_bytes INTEGER NOT NULL DEFAULT 0 CHECK (user_quota_bytes >= 0);

CREATE INDEX nodes_size ON nodes (folder_id, size DESC, id) WHERE kind = 'file';

-- An upload aimed at a directory belongs to the folder of that directory. Earlier moves could leave it
-- behind; bring it into line before the counters are built from it.
UPDATE uploads SET folder_id = (SELECT p.folder_id FROM nodes p WHERE p.id = uploads.parent_id)
	WHERE parent_id IS NOT NULL AND folder_id <> (SELECT p.folder_id FROM nodes p WHERE p.id = uploads.parent_id);

UPDATE folders SET
	used_bytes = (SELECT COALESCE(SUM(n.size), 0) FROM nodes n WHERE n.folder_id = folders.id AND n.kind = 'file')
		+ (SELECT COALESCE(SUM(v.size), 0) FROM versions v JOIN nodes n ON n.id = v.node_id WHERE n.folder_id = folders.id),
	used_files = (SELECT COUNT(*) FROM nodes n WHERE n.folder_id = folders.id AND n.kind = 'file'),
	reserved_bytes = (SELECT COALESCE(SUM(CASE WHEN u.visitor_id = '' THEN u.length ELSE u.durable_offset END), 0)
		FROM uploads u WHERE u.folder_id = folders.id);

-- The account's counters are the sum of its folders' (the invariant the sweep checks). Every node, upload and
-- version belongs to a folder: nodes.folder_id and uploads.folder_id are NOT NULL foreign keys, and a version
-- belongs to its node's folder. So the sum below leaves nothing out, and a drifted account is made right here.
UPDATE accounts SET
	used_bytes = (SELECT COALESCE(SUM(f.used_bytes), 0) FROM folders f WHERE f.account_id = accounts.id),
	used_files = (SELECT COALESCE(SUM(f.used_files), 0) FROM folders f WHERE f.account_id = accounts.id),
	reserved_bytes = (SELECT COALESCE(SUM(f.reserved_bytes), 0) FROM folders f WHERE f.account_id = accounts.id);
