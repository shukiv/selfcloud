-- The tags of an audio file: title, artist, album and duration, read on the server (design section 14, slice x3, decision 56).
--
-- One row per blob, so a new version of a file (a new blob) has its own tags and an old one's go with it: the cascade
-- removes a row with its blob, and with the account. The row holds the values only; the state of the read (queued, failed,
-- retried) is the derived row of kind audio_tags (migration 0028). A row exists only once the read succeeded.
--
-- Values are plain text, cut to 256 characters by the reader; the length checks here are a second bound on input that
-- the parent has already checked. Times are not kept: a row is replaced whole when its blob is read again.

CREATE TABLE audio_tags (
    blob_id     TEXT    PRIMARY KEY REFERENCES blobs (id) ON DELETE CASCADE,
    account_id  TEXT    NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    title       TEXT    NOT NULL DEFAULT '' CHECK (length(title) <= 256),
    artist      TEXT    NOT NULL DEFAULT '' CHECK (length(artist) <= 256),
    album       TEXT    NOT NULL DEFAULT '' CHECK (length(album) <= 256),
    duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms BETWEEN 0 AND 604800000)
) STRICT;

CREATE INDEX audio_tags_account ON audio_tags (account_id);
