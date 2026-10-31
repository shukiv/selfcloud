-- Notifications for mentions and replies, their mail outbox, and the user's email switch (design section 14, slice r,
-- decision 43).
--
-- A notification holds IDs, a kind and times. It holds no name and no text: the names are read when a user lists it, and
-- the text is read by the walk (decision 43). The outbox holds one row per notification that may be mailed. Its
-- last_error is a fixed code, never the server's text, so an address cannot be stored there.
--
-- notify_prefs holds the email switch and the mail budget of each user: the hour's and the day's charge. The budget is in
-- the database, not in memory, so it holds across a restart and across two instances. Users with no row have email on
-- and an empty budget.

CREATE TABLE notifications (
    id          TEXT    PRIMARY KEY,                            -- ntf_...
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT    NOT NULL CHECK (kind IN ('mention', 'reply')),
    node_id     TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    comment_id  TEXT    NOT NULL REFERENCES comments (id) ON DELETE CASCADE,
    actor_id    TEXT             REFERENCES users (id) ON DELETE SET NULL,
    created_at  INTEGER NOT NULL,
    read_at     INTEGER,
    UNIQUE (user_id, comment_id, kind)
) STRICT;

CREATE INDEX notifications_user ON notifications (user_id, created_at DESC, id DESC);
CREATE INDEX notifications_unread ON notifications (user_id, created_at DESC) WHERE read_at IS NULL;
CREATE INDEX notifications_account ON notifications (account_id);
CREATE INDEX notifications_created ON notifications (created_at);
CREATE INDEX notifications_node ON notifications (node_id);
CREATE INDEX notifications_comment ON notifications (comment_id);

CREATE TABLE notify_outbox (
    id              INTEGER PRIMARY KEY,
    account_id      TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id         TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    notification_id TEXT    NOT NULL UNIQUE REFERENCES notifications (id) ON DELETE CASCADE,
    state           TEXT    NOT NULL CHECK (state IN ('pending', 'sent', 'skipped', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at INTEGER NOT NULL,
    lease_until     INTEGER,
    last_error      TEXT    NOT NULL DEFAULT '' CHECK (length(last_error) <= 40),
    created_at      INTEGER NOT NULL,
    finished_at     INTEGER
) STRICT;

CREATE INDEX notify_outbox_due ON notify_outbox (state, next_attempt_at);
CREATE INDEX notify_outbox_user ON notify_outbox (user_id, state, next_attempt_at);
CREATE INDEX notify_outbox_account ON notify_outbox (account_id, state);
CREATE INDEX notify_outbox_done ON notify_outbox (state, finished_at);

CREATE TABLE notify_prefs (
    user_id        TEXT    PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    account_id     TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    email          INTEGER NOT NULL DEFAULT 1 CHECK (email IN (0, 1)),
    last_mail_at   INTEGER,                                    -- the last mail charged to the user, in millis
    mail_day_start INTEGER,                                    -- the start of the day the day count runs from
    mail_day_count INTEGER NOT NULL DEFAULT 0 CHECK (mail_day_count >= 0),
    updated_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX notify_prefs_account ON notify_prefs (account_id, last_mail_at);
