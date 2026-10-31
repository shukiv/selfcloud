// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Notifications (design section 14, slice r, decision 43). A notification tells a user that a comment named them or
// replied to their comment. It holds IDs, a kind and times: no name and no text. The outbox row of a notification is
// the mail queue of that notification. Both are written in the transaction that writes the comment.

// Kinds of notification.
const (
	NotifyMention = "mention"
	NotifyReply   = "reply"
)

// States of an outbox row.
const (
	OutboxPending = "pending"
	OutboxSent    = "sent"
	OutboxSkipped = "skipped"
	OutboxFailed  = "failed"
)

// Limits of the notification tables. Each is enforced where its rows are written, or by the sweep, and is stated in
// design section 14.
const (
	// NotifyAccountPendingLimit is how many outbox rows one account may hold waiting for mail. Over it no mail is queued
	// for that account's new notifications; the app still gets them, and other accounts are not affected (review D3).
	NotifyAccountPendingLimit = 10000
	// NotifyPendingLimit is how many outbox rows may wait for mail at once, over all accounts: a backstop only, so the
	// accounts together cannot exhaust the table. Over it a notification still reaches the app, and no mail is queued
	// for it (decision 43).
	NotifyPendingLimit = 100000
	// NotifyPerUser is how many notifications a user keeps. The oldest go when a new one passes it, in the same write.
	NotifyPerUser = 500
	// NotifyDayCap is how many mails a user may be charged in a rolling day.
	NotifyDayCap = 20
	// NotifyAccountHourCap is how many users of one account may be charged a mail in an hour.
	NotifyAccountHourCap = 200
)

// NewNotification is one notification to write, with its outbox row when mail is queued for it.
type NewNotification struct {
	ID        string
	AccountID string
	UserID    string
	Kind      string
	NodeID    string
	CommentID string
	ActorID   string // "" when the actor is unknown; the reader sees "former member" for a deleted actor
	At        time.Time
}

// NotifyRow is a notification as its reader lists it. The comment is live; the names are not in the row.
type NotifyRow struct {
	ID        string
	Kind      string
	NodeID    string
	ActorID   string // "" once the actor's user is deleted
	CreatedAt time.Time
	ReadAt    *time.Time
}

// InsertNotifications writes notifications, and a pending outbox row for each when mail is true, in the caller's write.
// The outbox rows stop at NotifyPendingLimit: past it a notification is written with no mail queued. Each user keeps at
// most NotifyPerUser notifications, the oldest removed in the same write.
func (tx *Tx) InsertNotifications(ctx context.Context, rows []NewNotification, mail bool) error {
	if len(rows) == 0 {
		return nil
	}
	// The caps are read once per call. Every row of a call has one account, as the comment's rows do.
	queue := false
	if mail {
		account, err := tx.countAccountPending(ctx, rows[0].AccountID, NotifyAccountPendingLimit)
		if err != nil {
			return err
		}
		all, err := tx.countOutboxPending(ctx, NotifyPendingLimit)
		if err != nil {
			return err
		}
		queue = account < NotifyAccountPendingLimit && all < NotifyPendingLimit
	}
	for _, n := range rows {
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO notifications
			(id, account_id, user_id, kind, node_id, comment_id, actor_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			n.ID, n.AccountID, n.UserID, n.Kind, n.NodeID, n.CommentID, nullString(n.ActorID), millis(n.At)); err != nil {
			return err
		}
		if queue {
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO notify_outbox
				(account_id, user_id, notification_id, state, next_attempt_at, created_at)
				VALUES (?, ?, ?, 'pending', ?, ?)`,
				n.AccountID, n.UserID, n.ID, millis(n.At), millis(n.At)); err != nil {
				return err
			}
		}
		if err := tx.capNotifications(ctx, n.UserID); err != nil {
			return err
		}
	}
	return nil
}

// countOutboxPending counts the pending outbox rows, reading at most limit of them.
func (tx *Tx) countOutboxPending(ctx context.Context, limit int) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, countOutboxPendingSQL, limit).Scan(&n)
	return n, err
}

// countAccountPending counts the pending outbox rows of one account, reading at most limit of them.
func (tx *Tx) countAccountPending(ctx context.Context, accountID string, limit int) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, countAccountPendingSQL, accountID, limit).Scan(&n)
	return n, err
}

// capNotifications removes the notifications of a user beyond NotifyPerUser, the oldest first. It reads at most one row
// past the cap, so its cost does not grow with the user's history.
func (tx *Tx) capNotifications(ctx context.Context, userID string) error {
	var n int
	if err := tx.q.QueryRowContext(ctx, countUserNotifySQL, userID, NotifyPerUser+1).Scan(&n); err != nil {
		return err
	}
	if n <= NotifyPerUser {
		return nil
	}
	_, err := tx.q.ExecContext(ctx, capDeleteSQL, userID, n-NotifyPerUser)
	return err
}

// The statements that more than one function uses, or that the plan test reads. Each is a constant, so the plan test reads
// the statement the store runs.
const (
	countOutboxPendingSQL = `SELECT COUNT(*) FROM (SELECT 1 FROM notify_outbox WHERE state = 'pending' LIMIT ?)`
	// countAccountPendingSQL reads one account's pending rows through notify_outbox_account (account_id, state).
	countAccountPendingSQL = `SELECT COUNT(*) FROM (SELECT 1 FROM notify_outbox WHERE account_id = ? AND state = 'pending' LIMIT ?)`
	countUserNotifySQL     = `SELECT COUNT(*) FROM (SELECT 1 FROM notifications WHERE user_id = ? LIMIT ?)`
	// oldestOverCapSQL names the oldest notifications of a user, and capDeleteSQL removes them.
	oldestOverCapSQL = `SELECT id FROM notifications WHERE user_id = ? ORDER BY created_at ASC, id ASC LIMIT ?`
	capDeleteSQL     = `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE user_id = ?
		ORDER BY created_at ASC, id ASC LIMIT ?)`
	// accountHourSQL counts the users of an account charged since a time, reading at most a cap of them.
	accountHourSQL = `SELECT COUNT(*) FROM (SELECT 1 FROM notify_prefs WHERE account_id = ? AND last_mail_at > ? LIMIT ?)`
	// claimUserRowsSQL is the due pending rows of one user, with what a mail needs of each.
	claimUserRowsSQL = `SELECT o.id, o.account_id, o.user_id, o.notification_id, n.kind, n.node_id,
			n.comment_id, COALESCE(n.actor_id, ''), o.attempts, o.created_at
		FROM notify_outbox o JOIN notifications n ON n.id = o.notification_id
		WHERE o.user_id = ? AND o.state = 'pending' AND o.next_attempt_at <= ?
			AND (o.lease_until IS NULL OR o.lease_until <= ?)
		ORDER BY o.next_attempt_at, o.id LIMIT ?`
	pruneNotifySQL = `DELETE FROM notifications WHERE id IN (
		SELECT id FROM notifications WHERE created_at < ? LIMIT ?)`
	expireMailSQL = `UPDATE notify_outbox SET state = 'skipped', last_error = 'expired',
		finished_at = ?, lease_until = NULL WHERE id IN (
		SELECT id FROM notify_outbox WHERE state = 'pending' AND created_at < ?
			AND (lease_until IS NULL OR lease_until <= ?) LIMIT ?)`
	pruneMailSQL = `DELETE FROM notify_outbox WHERE id IN (
		SELECT id FROM notify_outbox WHERE state IN ('sent', 'skipped', 'failed') AND finished_at < ? LIMIT ?)`
)

// notifyCols is the column list of the notification reads, in the order scanNotify reads it.
const notifyCols = `n.id, n.kind, n.node_id, COALESCE(n.actor_id, ''), n.created_at, n.read_at`

// scanNotify reads one row of notifyCols.
func scanNotify(rows interface{ Scan(...any) error }) (NotifyRow, error) {
	var r NotifyRow
	var created int64
	var read sql.NullInt64
	if err := rows.Scan(&r.ID, &r.Kind, &r.NodeID, &r.ActorID, &created, &read); err != nil {
		return r, err
	}
	r.CreatedAt = fromMillis(created)
	if read.Valid {
		t := fromMillis(read.Int64)
		r.ReadAt = &t
	}
	return r, nil
}

// notificationsSQL is the query of NotificationsPage, with the cursor clause when cursored. A notification of a comment
// that is a tombstone is left out: the comment is gone for its readers.
func notificationsSQL(cursored bool) string {
	q := `SELECT ` + notifyCols + ` FROM notifications n
		JOIN comments c ON c.id = n.comment_id
		WHERE n.account_id = ? AND n.user_id = ? AND c.deleted_at IS NULL`
	if cursored {
		q += ` AND (n.created_at < ? OR (n.created_at = ? AND n.id < ?))`
	}
	return q + ` ORDER BY n.created_at DESC, n.id DESC LIMIT ?`
}

// NotificationsPage lists a user's notifications, newest first, p.Limit at a time. The rows carry no name and no text;
// the caller checks reach to each node before it shows one.
func (s *Store) NotificationsPage(ctx context.Context, accountID, userID string, p Page) ([]NotifyRow, error) {
	args := []any{accountID, userID}
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 2)
		if err != nil {
			return nil, err
		}
		at, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return nil, ErrBadCursor
		}
		args = append(args, at, at, parts[1])
	}
	args = append(args, p.Limit)
	return s.queryNotify(ctx, notificationsSQL(p.Cursor != ""), args...)
}

// NotificationCursor is the cursor that resumes NotificationsPage after row r.
func NotificationCursor(r NotifyRow) string {
	return encodeCursor(strconv.FormatInt(millis(r.CreatedAt), 10), r.ID)
}

// unreadSQL is the query of UnreadNewest. The partial index notifications_unread answers it.
const unreadSQL = `SELECT ` + notifyCols + ` FROM notifications n
	JOIN comments c ON c.id = n.comment_id
	WHERE n.account_id = ? AND n.user_id = ? AND n.read_at IS NULL AND c.deleted_at IS NULL
	ORDER BY n.created_at DESC, n.id DESC LIMIT ?`

// UnreadNewest returns up to limit unread notifications of a user, newest first. The count the bell shows is made from
// these, after the reach check, so it reads at most limit rows whatever the user's history.
func (s *Store) UnreadNewest(ctx context.Context, accountID, userID string, limit int) ([]NotifyRow, error) {
	return s.queryNotify(ctx, unreadSQL, accountID, userID, limit)
}

// queryNotify runs a notification query and reads its rows.
func (s *Store) queryNotify(ctx context.Context, query string, args ...any) ([]NotifyRow, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotifyRow
	for rows.Next() {
		r, err := scanNotify(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkNotificationRead sets the read time of a notification of the user in the account. Another user's notification, and
// one of another account, are ErrNotFound: the write touches the caller's own rows only.
func (tx *Tx) MarkNotificationRead(ctx context.Context, accountID, userID, id string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE notifications SET read_at = COALESCE(read_at, ?)
		WHERE account_id = ? AND user_id = ? AND id = ?`, millis(at), accountID, userID, id))
}

// NotifyEmailOn reports whether a user's email is on. A user with no preference row has it on.
func (s *Store) NotifyEmailOn(ctx context.Context, userID string) (bool, error) {
	return notifyEmailOn(ctx, s.r, userID)
}

// NotifyEmailOn is Store.NotifyEmailOn read inside the transaction.
func (tx *Tx) NotifyEmailOn(ctx context.Context, userID string) (bool, error) {
	return notifyEmailOn(ctx, tx.q, userID)
}

func notifyEmailOn(ctx context.Context, q querier, userID string) (bool, error) {
	var on int64
	err := q.QueryRowContext(ctx, "SELECT email FROM notify_prefs WHERE user_id = ?", userID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return on == 1, err
}

// SetNotifyEmail sets a user's email switch. It keeps the user's mail budget, which an upsert does not touch.
func (tx *Tx) SetNotifyEmail(ctx context.Context, accountID, userID string, on bool, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO notify_prefs (user_id, account_id, email, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET email = excluded.email, updated_at = excluded.updated_at`,
		userID, accountID, boolInt(on), millis(at))
	return err
}

// MailJob is one outbox row a worker has claimed, with what its mail needs. Lease is the lease the claim wrote; an
// outcome applies only while the row still holds that lease.
type MailJob struct {
	OutboxID       int64
	AccountID      string
	UserID         string
	NotificationID string
	Kind           string
	NodeID         string
	CommentID      string
	ActorID        string
	Attempts       int
	Lease          int64
	// CreatedAt is when the row was queued. A row older than the mail's age limit is not sent (review D5).
	CreatedAt time.Time
}

// claimUsersSQL ranks the users with a due pending row, one account after another, and leaves out the users whose mail
// budget is spent, so a worker does not claim rows it must hand back. The budget is checked again when a mail is charged.
const claimUsersSQL = `WITH due AS (
	SELECT o.user_id AS user_id, o.account_id AS account_id, MIN(o.next_attempt_at) AS nat
	FROM notify_outbox o
	LEFT JOIN notify_prefs p ON p.user_id = o.user_id
	WHERE o.state = 'pending' AND o.next_attempt_at <= ? AND (o.lease_until IS NULL OR o.lease_until <= ?)
		AND (p.last_mail_at IS NULL OR p.last_mail_at <= ?)
		AND (p.mail_day_start IS NULL OR p.mail_day_start <= ? OR p.mail_day_count < ?)
	GROUP BY o.user_id, o.account_id
), ranked AS (
	SELECT user_id, nat, ROW_NUMBER() OVER (PARTITION BY account_id ORDER BY nat, user_id) AS rnk FROM due
)
SELECT user_id FROM ranked ORDER BY rnk, nat, user_id LIMIT ?`

// ClaimMail claims the due pending mail of up to users users at at, and at most perUser rows of each, under a lease of
// lease. One write does it, so two claims never hold one row. The rows come back grouped by user, the users ranked by
// account, so one account's backlog does not take every claim of another's.
func (s *Store) ClaimMail(ctx context.Context, at time.Time, lease time.Duration, users, perUser int) ([]MailJob, error) {
	var out []MailJob
	err := s.Write(ctx, func(tx *Tx) error {
		now := millis(at)
		dayCut := millis(at.Add(-24 * time.Hour))
		hourCut := millis(at.Add(-time.Hour))
		rows, err := tx.q.QueryContext(ctx, claimUsersSQL, now, now, hourCut, dayCut, NotifyDayCap, users)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, u)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		leaseUntil := millis(at.Add(lease))
		for _, u := range ids {
			got, err := tx.claimUserMail(ctx, u, now, leaseUntil, perUser)
			if err != nil {
				return err
			}
			out = append(out, got...)
		}
		return nil
	})
	return out, err
}

// claimUserMail leases up to perUser due pending rows of one user, and returns them with what each mail needs.
func (tx *Tx) claimUserMail(ctx context.Context, userID string, now, leaseUntil int64, perUser int) ([]MailJob, error) {
	rows, err := tx.q.QueryContext(ctx, claimUserRowsSQL, userID, now, now, perUser)
	if err != nil {
		return nil, err
	}
	var out []MailJob
	for rows.Next() {
		var j MailJob
		var created int64
		if err := rows.Scan(&j.OutboxID, &j.AccountID, &j.UserID, &j.NotificationID, &j.Kind, &j.NodeID,
			&j.CommentID, &j.ActorID, &j.Attempts, &created); err != nil {
			rows.Close()
			return nil, err
		}
		j.CreatedAt = fromMillis(created)
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		out[i].Lease = leaseUntil
		if _, err := tx.q.ExecContext(ctx, `UPDATE notify_outbox SET lease_until = ? WHERE id = ?`,
			leaseUntil, out[i].OutboxID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ChargeMail charges one mail to a user's budget at at, or says why it may not go. The hour allows one mail per user; the
// day allows NotifyDayCap in a rolling day; the account allows NotifyAccountHourCap users to be charged in an hour. A
// refused charge says when the next one may come (retry). Two instances cannot both pass: the charge is one write.
func (s *Store) ChargeMail(ctx context.Context, accountID, userID string, at time.Time) (ok bool, reason string, retry time.Time, err error) {
	err = s.Write(ctx, func(tx *Tx) error {
		var e error
		ok, reason, retry, e = tx.chargeMail(ctx, accountID, userID, at)
		return e
	})
	return ok, reason, retry, err
}

// chargeMail is ChargeMail inside a write.
func (tx *Tx) chargeMail(ctx context.Context, accountID, userID string, at time.Time) (bool, string, time.Time, error) {
	if _, err := tx.q.ExecContext(ctx, `INSERT OR IGNORE INTO notify_prefs (user_id, account_id, updated_at)
		VALUES (?, ?, ?)`, userID, accountID, millis(at)); err != nil {
		return false, "", time.Time{}, err
	}
	var last, dayStart sql.NullInt64
	var dayCount int
	if err := tx.q.QueryRowContext(ctx, `SELECT last_mail_at, mail_day_start, mail_day_count
		FROM notify_prefs WHERE user_id = ?`, userID).Scan(&last, &dayStart, &dayCount); err != nil {
		return false, "", time.Time{}, err
	}
	if last.Valid && fromMillis(last.Int64).Add(time.Hour).After(at) {
		return false, "hour", fromMillis(last.Int64).Add(time.Hour), nil
	}
	if !dayStart.Valid || !fromMillis(dayStart.Int64).Add(24*time.Hour).After(at) {
		dayStart = sql.NullInt64{Int64: millis(at), Valid: true}
		dayCount = 0
	}
	if dayCount >= NotifyDayCap {
		return false, "day", fromMillis(dayStart.Int64).Add(24 * time.Hour), nil
	}
	var mailed int
	if err := tx.q.QueryRowContext(ctx, accountHourSQL, accountID, millis(at.Add(-time.Hour)),
		NotifyAccountHourCap).Scan(&mailed); err != nil {
		return false, "", time.Time{}, err
	}
	if mailed >= NotifyAccountHourCap {
		return false, "account", at.Add(10 * time.Minute), nil
	}
	_, err := tx.q.ExecContext(ctx, `UPDATE notify_prefs SET last_mail_at = ?, mail_day_start = ?, mail_day_count = ?,
		updated_at = ? WHERE user_id = ?`, millis(at), dayStart.Int64, dayCount+1, millis(at), userID)
	return err == nil, "", time.Time{}, err
}

// NotifyOutcome is what a worker records for one claimed row. Its write applies only while the row holds Lease.
type NotifyOutcome struct {
	OutboxID int64
	Lease    int64
	State    string // OutboxSent, OutboxSkipped, OutboxFailed, or "" to put the row back for a later try
	Reason   string // a fixed code, at most 40 characters
	At       time.Time
	// NextAt is when a row put back, or one that failed and is retried, is due next.
	NextAt time.Time
	// Attempt is set when the row's send was tried and failed. It adds one to the row's attempts, whatever the state.
	Attempt bool
}

// FinishMail records one outcome. A row whose lease has ended, because it was claimed again, changes nothing.
func (s *Store) FinishMail(ctx context.Context, o NotifyOutcome) error {
	return s.Write(ctx, func(tx *Tx) error {
		switch {
		case o.State == OutboxSent || o.State == OutboxSkipped || o.State == OutboxFailed:
			_, err := tx.q.ExecContext(ctx, `UPDATE notify_outbox SET state = ?, lease_until = NULL, last_error = ?,
				finished_at = ?, attempts = attempts + ? WHERE id = ? AND lease_until = ?`,
				o.State, o.Reason, millis(o.At), boolInt(o.Attempt), o.OutboxID, o.Lease)
			return err
		default:
			_, err := tx.q.ExecContext(ctx, `UPDATE notify_outbox SET lease_until = NULL, next_attempt_at = ?,
				last_error = ?, attempts = attempts + ? WHERE id = ? AND lease_until = ?`,
				millis(o.NextAt), o.Reason, boolInt(o.Attempt), o.OutboxID, o.Lease)
			return err
		}
	})
}

// PruneNotifications removes the notifications created before cutoff, batch at a time, with their outbox rows.
func (s *Store) PruneNotifications(ctx context.Context, cutoff time.Time, batch int) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.q.ExecContext(ctx, pruneNotifySQL, millis(cutoff), batch)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// ExpireMail ends the pending mail created before cutoff as skipped, batch at a time. Mail that has waited this long is
// not worth sending late, and this bounds how long a row waits.
func (s *Store) ExpireMail(ctx context.Context, cutoff, at time.Time, batch int) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		// A row a worker holds is left alone: its send is in flight, and its outcome decides the row.
		res, err := tx.q.ExecContext(ctx, expireMailSQL, millis(at), millis(cutoff), millis(at), batch)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// PruneMailRows removes the finished outbox rows (sent, skipped or failed) that finished before cutoff, batch at a time.
func (s *Store) PruneMailRows(ctx context.Context, cutoff time.Time, batch int) (int64, error) {
	var n int64
	err := s.Write(ctx, func(tx *Tx) error {
		res, err := tx.q.ExecContext(ctx, pruneMailSQL, millis(cutoff), batch)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}
