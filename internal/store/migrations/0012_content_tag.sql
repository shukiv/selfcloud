-- Content tag (slice k2 security review). A file's content tag is a random value that changes on every
-- content write: a PUT of content, an upload that replaces, a version restore. A signed link is bound to
-- it, so a link dies when the content changes and stays dead when the same bytes come back. A rename or
-- move does not change it. Existing files take their current etag, which is random per write.
ALTER TABLE nodes ADD COLUMN content_tag TEXT NOT NULL DEFAULT '';
UPDATE nodes SET content_tag = etag WHERE kind = 'file';
