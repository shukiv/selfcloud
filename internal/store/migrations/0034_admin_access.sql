-- Installation admin access to tenant users (design section 8, decision 41).
--
-- A session made by sign-in-as carries the admin who made it in impersonator_user_id, and the
-- hash of the admin's own session in impersonator_session_hash. The impersonation ends one hour
-- after its creation (see internal/api/impersonation.go). Removing the admin deletes the rows
-- that name it, through the cascade.
--
-- must_change_password is set by a typed password (decision 41). Until the user changes it, the
-- user reaches only the change-password route and GET /me.

ALTER TABLE sessions ADD COLUMN impersonator_user_id TEXT REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE sessions ADD COLUMN impersonator_session_hash BLOB;

ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0
	CHECK (must_change_password IN (0, 1));
