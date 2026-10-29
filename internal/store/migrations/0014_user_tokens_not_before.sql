-- A per-user cut-off for delegated tokens: tokens a user's token issued at or before this time (Unix
-- milliseconds) are void. Revoking one user moves it forward; the account-wide cut-off is unchanged.
-- 0 means no revocation.

ALTER TABLE users ADD COLUMN tokens_not_before INTEGER NOT NULL DEFAULT 0;
