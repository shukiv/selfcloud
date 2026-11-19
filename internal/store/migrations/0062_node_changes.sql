-- The changes journal (slice cf1, decision 235): one row per write that changes a node, so a client can ask what
-- changed in a space since a token and follow it with a long poll.
--
-- A row names the node, the folder it was in when the write landed, and the account that folder belongs to. relist
-- is set when a directory comes back from the trash: the rows of the nodes below it did not change, so a client
-- must list the directory again to learn them. at is Unix milliseconds, read from the database clock in the
-- trigger, not from the node's own times.
--
-- The journal holds no foreign keys on purpose. A purge, a folder delete and an account delete take the nodes with
-- them, but their rows stay so a feed still reports the removal; the sweep prunes rows past changes.retention and
-- rows whose account is gone, and raises node_changes_mark to the highest seq it removed. A cursor below the mark
-- is refused with gone, and the client starts again.
--
-- The triggers fire on the writes the feed reports. The UPDATE lists the columns a change of them means; a write of
-- a column not listed (name_key alone, the search or audit columns) leaves no row. A move to another folder leaves
-- a row for both folders, the destination's first. The DELETE fires for a cascade too, so a purge of a directory
-- leaves a row for every node below it.

CREATE TABLE node_changes (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT    NOT NULL,
    folder_id  TEXT    NOT NULL,
    node_id    TEXT    NOT NULL,
    relist     INTEGER NOT NULL DEFAULT 0 CHECK (relist IN (0, 1)),
    at         INTEGER NOT NULL
) STRICT;

CREATE INDEX node_changes_folder ON node_changes (folder_id, seq);
CREATE INDEX node_changes_account ON node_changes (account_id, seq);

-- The single mark row: the highest seq the sweep has pruned. A cursor at or below it can no longer be served.
CREATE TABLE node_changes_mark (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    pruned_through INTEGER NOT NULL
) STRICT;

INSERT INTO node_changes_mark (id, pruned_through) VALUES (1, 0);

CREATE TRIGGER nodes_changes_insert AFTER INSERT ON nodes
BEGIN
    INSERT INTO node_changes (account_id, folder_id, node_id, relist, at)
    VALUES (NEW.account_id, NEW.folder_id, NEW.id, 0, CAST(unixepoch('subsec') * 1000 AS INTEGER));
END;

CREATE TRIGGER nodes_changes_update AFTER UPDATE OF parent_id, folder_id, name, kind, size, mime_type, etag,
    content_tag, modified_at, trashed_at, no_download, color, description, target_id, owner_id ON nodes
BEGIN
    INSERT INTO node_changes (account_id, folder_id, node_id, relist, at)
    VALUES (NEW.account_id, NEW.folder_id, NEW.id,
        CASE WHEN NEW.kind = 'dir' AND OLD.trashed_at IS NOT NULL AND NEW.trashed_at IS NULL THEN 1 ELSE 0 END,
        CAST(unixepoch('subsec') * 1000 AS INTEGER));
    INSERT INTO node_changes (account_id, folder_id, node_id, relist, at)
    SELECT NEW.account_id, OLD.folder_id, NEW.id, 0, CAST(unixepoch('subsec') * 1000 AS INTEGER)
    WHERE OLD.folder_id <> NEW.folder_id;
END;

CREATE TRIGGER nodes_changes_delete AFTER DELETE ON nodes
BEGIN
    INSERT INTO node_changes (account_id, folder_id, node_id, relist, at)
    VALUES (OLD.account_id, OLD.folder_id, OLD.id, 0, CAST(unixepoch('subsec') * 1000 AS INTEGER));
END;