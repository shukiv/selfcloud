-- Share and access-request notifications join the list (design section 14, decision 182). They live in their own table,
-- not in notifications: decision 120 keeps migrations from 0051 on additive, so an older binary still reads the rows it
-- knows. notifications.comment_id is NOT NULL and its kind CHECK admits only mention and reply, and widening either would
-- rebuild the table, which an older binary's notification list could not read. A second table keeps that guarantee. The
-- reads union the two, so the list, the bell and the cursor treat them as one.
--
-- A share or a request names no comment, so there is no comment_id here. request_id names the access request a notice is
-- about, and is null for a share. The id comes from the same ntf_ generator as notifications, so the two share one id space
-- and a mark-read by id finds its row in whichever table holds it.

CREATE TABLE share_notifications (
    id          TEXT    PRIMARY KEY,                            -- ntf_...
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT    NOT NULL CHECK (kind IN ('share', 'access_request')),
    node_id     TEXT    NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    request_id  TEXT             REFERENCES access_requests (id) ON DELETE CASCADE,
    actor_id    TEXT             REFERENCES users (id) ON DELETE SET NULL,
    created_at  INTEGER NOT NULL,
    read_at     INTEGER
) STRICT;

CREATE INDEX share_notifications_user ON share_notifications (user_id, created_at DESC, id DESC);
CREATE INDEX share_notifications_unread ON share_notifications (user_id, created_at DESC) WHERE read_at IS NULL;
CREATE INDEX share_notifications_account ON share_notifications (account_id);
CREATE INDEX share_notifications_created ON share_notifications (created_at);