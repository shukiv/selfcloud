-- The WOPI user info (design section 10, decision 87, slice l5). PutUserInfo stores one string per person, which
-- CheckFileInfo returns to the editor as UserInfo. It belongs to the user, not to a file: the row goes with the user
-- (ON DELETE CASCADE) and is never shown to anyone else. The string is at most 1024 characters, as the WOPI page says;
-- the byte limit is checked by the API, and length() here is the same for ASCII.

CREATE TABLE wopi_user_info (
    user_id    TEXT    PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    account_id TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    info       TEXT    NOT NULL CHECK (length(info) <= 1024),
    updated_at INTEGER NOT NULL
) STRICT;
