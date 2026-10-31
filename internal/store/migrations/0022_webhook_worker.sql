-- Webhook worker (design section 10, slice j3, decision 28).
--
-- pending_count is the number of pending deliveries of an endpoint. The writer keeps it: EmitEvent adds to it, and
-- every change of a delivery from pending (delivered, failed) takes from it. The overflow check reads it in O(1),
-- where a count of the pending rows would walk up to 100,000 index entries for each event.
--
-- first_attempt_at is the first time the delivery was sent. The 24-hour give-up counts from it, not from created_at:
-- a delivery can wait behind a failing one for hours before it is first sent.
--
-- The three indexes serve the worker and the sweep. (endpoint_id, state, event_row_id) finds the head of an
-- endpoint's queue, the oldest pending delivery, with a seek. (state, finished_at) finds the delivered and failed rows
-- past their retention. (event_row_id) finds the deliveries of an event, so an event no delivery names can be found.

ALTER TABLE webhook_endpoints ADD COLUMN pending_count INTEGER NOT NULL DEFAULT 0 CHECK (pending_count >= 0);

UPDATE webhook_endpoints SET pending_count =
    (SELECT COUNT(*) FROM webhook_deliveries d WHERE d.endpoint_id = webhook_endpoints.id AND d.state = 'pending');

ALTER TABLE webhook_deliveries ADD COLUMN first_attempt_at INTEGER;

-- generation counts the disables of an endpoint. A delivery takes the generation of its endpoint when it is queued
-- (fan-out, retry, test). A disable raises the endpoint's generation, so the deliveries queued before it are stale: the
-- worker never sends them, and the cleanup fails them whatever the endpoint's state is by then (decision 28).
-- stale_queue is 1 while an endpoint may still hold pending deliveries of an earlier generation.
ALTER TABLE webhook_endpoints ADD COLUMN generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0);
ALTER TABLE webhook_endpoints ADD COLUMN stale_queue INTEGER NOT NULL DEFAULT 0 CHECK (stale_queue IN (0, 1));
ALTER TABLE webhook_deliveries ADD COLUMN generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0);

CREATE INDEX webhook_deliveries_head ON webhook_deliveries (endpoint_id, state, generation, event_row_id);
CREATE INDEX webhook_deliveries_finished ON webhook_deliveries (state, finished_at);
CREATE INDEX webhook_deliveries_event ON webhook_deliveries (event_row_id);
-- The leases still held, per endpoint: a claim refuses an endpoint with an unexpired one. Small, since a lease ends with its outcome.
CREATE INDEX webhook_deliveries_lease ON webhook_deliveries (endpoint_id, lease_until) WHERE lease_until IS NOT NULL;
