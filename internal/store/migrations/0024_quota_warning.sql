-- Quota warnings (design section 10, slice j4, decision 30).
--
-- quota_warning_percent is the level at which quota.warning is sent, as a share of the account's quota and of each
-- user's limit. quota.exceeded is at 100 percent and has no setting. The default is 80. The quota marks table
-- (0020) holds one row for each level a scope is past; its rows exist only while the account is watched.
--
-- quota_watched is 1 while the account has an active endpoint that subscribes to a quota event. The endpoint writes keep
-- it (webhook_quota.go, syncQuotaWatch), so a usage change reads one column of the account row it already reads, and no
-- query asks about endpoints on the hot path.

ALTER TABLE accounts ADD COLUMN quota_warning_percent INTEGER NOT NULL DEFAULT 80
	CHECK (quota_warning_percent BETWEEN 1 AND 99);

ALTER TABLE accounts ADD COLUMN quota_watched INTEGER NOT NULL DEFAULT 0 CHECK (quota_watched IN (0, 1));

UPDATE accounts SET quota_watched = 1 WHERE EXISTS (
	SELECT 1 FROM webhook_endpoint_events s JOIN webhook_endpoints e ON e.id = s.endpoint_id AND e.account_id = s.account_id
	WHERE s.account_id = accounts.id AND e.state = 'active' AND s.event IN ('quota.warning', 'quota.exceeded'));
