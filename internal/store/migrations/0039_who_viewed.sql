-- Who viewed a file (design section 14, slice r5, decision 51). The list reads the recent table, one row per user and
-- file, for the rows that hold a view, on one node: an equality on node_id and a range on viewed_at. The index costs one
-- more write on each view batch.
--
-- The account's switch is off by default. Only a manager of the node and the account key read the list, and only while
-- the switch is on (the route refuses otherwise).

CREATE INDEX recent_node ON recent (node_id, viewed_at);

ALTER TABLE accounts ADD COLUMN who_viewed INTEGER NOT NULL DEFAULT 0 CHECK (who_viewed IN (0, 1));

-- When the switch last went on, in milliseconds; empty while it is off. The recent rows are written whatever the switch
-- says, since they are each user's own Recent list. The list starts here, so that turning the switch on does not show
-- views made while nobody had been told they would be shown.
ALTER TABLE accounts ADD COLUMN who_viewed_since INTEGER;
