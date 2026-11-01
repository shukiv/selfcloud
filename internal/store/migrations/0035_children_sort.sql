-- Server-side sorting of folder listings (design section 14, slice sort1, decision 47).
--
-- A listing sorted by name, size or modified time reads one index per kind: the rows of parent_id with kind = ?, in the
-- sort's order, then by id. The three kinds are separate segments: folders, then shortcuts, then files, whatever the
-- sort direction. The index is partial on trashed_at IS NULL, so a trashed node takes no room in it. A descending sort
-- reads the same index backwards.
--
-- name uses the collation selfcloud_name (internal/names Compare, registered in internal/store/collation.go). The
-- collation is part of the index: it must not change without a migration that rebuilds these three indexes.
--
-- The indexes are built here on the whole nodes table. Their cost is one pass over the table per index, and about
-- 40 to 60 bytes per row for size and modified_at and more for name, which grows with name length.

CREATE INDEX nodes_list_name ON nodes (parent_id, kind, name COLLATE selfcloud_name, id) WHERE trashed_at IS NULL;
CREATE INDEX nodes_list_size ON nodes (parent_id, kind, size, id) WHERE trashed_at IS NULL;
CREATE INDEX nodes_list_modified ON nodes (parent_id, kind, modified_at, id) WHERE trashed_at IS NULL;
