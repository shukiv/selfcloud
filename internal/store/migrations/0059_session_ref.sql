-- An application can end the web sessions its sign-in links started (decision 211). A sign-in link may carry a
-- session_ref, the application's own name for the session it starts, and a session_ends_at, the moment that session
-- must end; the redeemed session copies both. The account's own key ends the user's sessions of one ref, or all of
-- them, without touching any token. Existing rows are links and sessions with no ref and no end, as they were.
-- Decision 120 keeps this additive: four columns and one index.

ALTER TABLE sign_in_links ADD COLUMN session_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_in_links ADD COLUMN session_ends_at INTEGER;
ALTER TABLE sessions ADD COLUMN session_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN ends_at INTEGER;
CREATE INDEX sessions_user_ref ON sessions (user_id, session_ref) WHERE session_ref != '';