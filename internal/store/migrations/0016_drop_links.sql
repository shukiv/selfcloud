-- Drop links: a link on a directory that lets a visitor upload files there and nothing else. Times are
-- Unix milliseconds.
--
-- A drop link is an anyone link with upload_only = 1 and no role. The node has at most one drop link
-- besides its access link (UNIQUE (node_id, upload_only) in 0015). Its uploads carry the visitor's ID, so
-- one visitor cannot reach another's uploads through the same link.
--
-- The title, message and ask_name fields are shown to the visitor. notify turns on the owner's mail: each
-- file that arrives adds one to unreported_files, and the notifier mails the count at most once an hour
-- and clears what it reported. notified_at is the last mail's time, or NULL before the first.

ALTER TABLE share_links ADD COLUMN title TEXT NOT NULL DEFAULT '' CHECK (length(title) <= 100);
ALTER TABLE share_links ADD COLUMN message TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 1000);
ALTER TABLE share_links ADD COLUMN ask_name INTEGER NOT NULL DEFAULT 0 CHECK (ask_name IN (0, 1));
ALTER TABLE share_links ADD COLUMN notify INTEGER NOT NULL DEFAULT 0 CHECK (notify IN (0, 1));
ALTER TABLE share_links ADD COLUMN unreported_files INTEGER NOT NULL DEFAULT 0 CHECK (unreported_files >= 0);
ALTER TABLE share_links ADD COLUMN notified_at INTEGER;

-- Who sent an upload through a drop link. The name and email are what the visitor typed, never verified.
-- The visitor ID is the one the credential carries; it is empty for every other upload.
ALTER TABLE uploads ADD COLUMN uploader_name TEXT NOT NULL DEFAULT '' CHECK (length(uploader_name) <= 100);
ALTER TABLE uploads ADD COLUMN uploader_email TEXT NOT NULL DEFAULT '' CHECK (length(uploader_email) <= 254);
ALTER TABLE uploads ADD COLUMN visitor_id TEXT NOT NULL DEFAULT '';

CREATE INDEX share_links_notify ON share_links (notify, upload_only) WHERE notify = 1;
