-- Whether a web session is one the browser did not keep (decision 75, "Keep me signed in" unticked).
--
-- short = 1 is a session with a browser-session cookie (no Max-Age, so the browser drops it when it closes), which the
-- server ends sessions.short_idle_timeout after its last use, or at its absolute limit, whichever comes first. short = 0
-- is a kept session, which keeps today's lifetime. Existing rows are kept sessions, as they were.

ALTER TABLE sessions ADD COLUMN short INTEGER NOT NULL DEFAULT 0 CHECK (short IN (0, 1));
