-- Office editing locks (design section 10, slice l3, decision 32).
--
-- wopi_sessions.served_tag is the content tag of the file as the editing session was given it: set when the session
-- opens, and moved on by each PutFile the session makes. Lock refuses a session whose tag is not the file's current
-- tag, so an API write made after the editor loaded the file is not overwritten. NULL (a row from before this
-- migration) is refused the same way.
--
-- wopi_locks.version_id is the version that the lock's editing session made at its first save. The session's later
-- saves replace the content without making another version. The version is cleared when it is pruned.

ALTER TABLE wopi_sessions ADD COLUMN served_tag TEXT;

ALTER TABLE wopi_locks ADD COLUMN version_id TEXT REFERENCES versions (id) ON DELETE SET NULL;

CREATE INDEX wopi_locks_account ON wopi_locks (account_id, expires_at);
