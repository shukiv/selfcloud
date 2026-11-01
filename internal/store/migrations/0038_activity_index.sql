-- The activity pane of a node (design section 14, slice r4, decision 50).
--
-- The pane reads the audit entries whose target is one node: an equality on account_id and target, newest first, the
-- rows after a cursor by id. audit_log has had indexes on (account_id, id) and on time only, so such a read would walk
-- every entry of the account. id is the table's rowid, so it is the last column of every index already, and the order
-- by id descending and the cursor's range on id are served by the index without a sort. The index costs one more write
-- on each audit insert.
--
-- The index is built here on the whole table, one pass over it.

CREATE INDEX audit_log_target ON audit_log (account_id, target);
