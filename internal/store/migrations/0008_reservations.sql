-- Quota reservations. Each unfinished upload holds its declared length in accounts.reserved_bytes,
-- from creation until it completes or ends. Before this migration nothing counted reservations, so
-- the counters of uploads already in progress are filled in here. `selfcloud admin recount-usage`
-- recomputes them from the uploads table at any time.

UPDATE accounts SET reserved_bytes = (
    SELECT COALESCE(SUM(u.length), 0) FROM uploads u WHERE u.account_id = accounts.id
);
