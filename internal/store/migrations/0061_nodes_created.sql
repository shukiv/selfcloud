-- The newest-first search order (decision 234).
--
-- A filter-only name search with order=newest reads one account's nodes by created time, newest first, without sorting
-- them: the index gives the created order for the account, and the rowid the index carries breaks ties, so a reverse
-- scan is the order the query asks for. It is partial on trashed_at IS NULL, the same condition every search applies,
-- so a trashed node is not in it. The account column comes first so one account's seek never reads another's rows.

CREATE INDEX nodes_created ON nodes (account_id, created_at) WHERE trashed_at IS NULL;