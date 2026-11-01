-- runner: foreign_keys=off
-- Decision 48. audit_log.account_id becomes a foreign key with ON DELETE CASCADE, so deleting an account deletes its
-- entries. Installation-wide entries keep account_id NULL, which a foreign key allows. The record of a deletion is kept
-- at installation level: an account.delete or account.deleted entry of an account already deleted, or still being
-- deleted, moves to NULL, and where the account still exists its name becomes the target. Other entries of an account
-- already deleted are removed, or the rebuild would fail on them. SQLite cannot add a foreign key to a table, so the
-- table is rebuilt and its indexes are created again. audit_log_time serves the retention sweep.

UPDATE audit_log SET target = (SELECT name FROM accounts WHERE accounts.id = audit_log.account_id)
 WHERE action IN ('account.delete', 'account.deleted')
   AND account_id IN (SELECT id FROM accounts WHERE status = 'deleting');

UPDATE audit_log SET account_id = NULL
 WHERE action IN ('account.delete', 'account.deleted') AND account_id IS NOT NULL
   AND (account_id NOT IN (SELECT id FROM accounts)
        OR account_id IN (SELECT id FROM accounts WHERE status = 'deleting'));

DELETE FROM audit_log WHERE account_id IS NOT NULL AND account_id NOT IN (SELECT id FROM accounts);

CREATE TABLE audit_log_new (
    id         INTEGER PRIMARY KEY, -- increases with time; the paging cursor
    time       INTEGER NOT NULL,
    account_id TEXT    REFERENCES accounts (id) ON DELETE CASCADE, -- NULL: installation-wide
    actor_kind TEXT    NOT NULL,
    actor_id   TEXT    NOT NULL,
    action     TEXT    NOT NULL,
    target     TEXT    NOT NULL DEFAULT '',
    ip         TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    outcome    TEXT    NOT NULL DEFAULT 'ok',
    details    TEXT, -- JSON object
    private_user_id TEXT
) STRICT;

INSERT INTO audit_log_new (id, time, account_id, actor_kind, actor_id, action, target, ip, user_agent, outcome,
        details, private_user_id)
    SELECT id, time, account_id, actor_kind, actor_id, action, target, ip, user_agent, outcome,
        details, private_user_id FROM audit_log;

DROP TABLE audit_log;

ALTER TABLE audit_log_new RENAME TO audit_log;

CREATE INDEX audit_log_account ON audit_log (account_id, id);

CREATE INDEX audit_log_time ON audit_log (time);
