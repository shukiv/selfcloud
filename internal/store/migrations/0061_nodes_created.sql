-- The newest-first search order (decision 234).
--
-- A filter-only name search with order=newest reads one account's nodes by created time, newest first, without sorting
-- them: the index gives the created order for the account, and the rowid the index carries breaks ties, so a reverse
-- scan is the order the query asks for. It is partial on trashed_at IS NULL, the same condition every search applies,
-- so a trashed node is not in it. The account column comes first so one account's seek never reads another's rows.
--
-- The partial condition carries a second term, created_at > -1, that only the newest order writes into its WHERE.
-- SQLite uses a partial index only when the query's WHERE contains its terms, so no other query — a plain account read,
-- a children listing, a recursive subtree walk — can be planned onto this index. created_at is a unix millisecond and
-- always > -1, so the term drops no row; it is a lock, not a filter. Without it the planner drew six recursive subtree
-- walks onto the index and scanned the account once per level (decision 234).

CREATE INDEX nodes_created ON nodes (account_id, created_at) WHERE trashed_at IS NULL AND created_at > -1;