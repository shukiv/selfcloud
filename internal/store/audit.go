// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// AuditEntry records who did what, when and from where.
type AuditEntry struct {
	ID        int64
	Time      time.Time
	AccountID string // "" for installation-wide actions
	ActorKind string // admin_key, account_key, user, token, system
	ActorID   string
	Action    string // e.g. "account.create"
	Target    string // the ID acted on
	IP        string
	UserAgent string
	Outcome   string // "ok" unless stated
	Details   map[string]any
	// PrivateUserID is the user an entry is private to: entries about a personal folder name its
	// owner, and only that user and the account key read them. "" for everything else.
	PrivateUserID string
	// Folders are the folders the entry is about, given by the caller. InsertAudit sets
	// PrivateUserID from the first of them that is a personal folder. Not stored.
	Folders []string
}

// InsertAudit appends e to the audit log and sets e.ID.
func (tx *Tx) InsertAudit(ctx context.Context, e *AuditEntry) error {
	if e.Outcome == "" {
		e.Outcome = "ok"
	}
	var details any
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return err
		}
		details = string(b)
	}
	if e.PrivateUserID == "" {
		for _, f := range e.Folders {
			var owner sql.NullString
			err := tx.q.QueryRowContext(ctx, `SELECT owner_user_id FROM folders
				WHERE account_id = ? AND id = ? AND kind = 'personal'`, e.AccountID, f).Scan(&owner)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			e.PrivateUserID = owner.String
			break
		}
	}
	res, err := tx.q.ExecContext(ctx, `INSERT INTO audit_log
		(time, account_id, actor_kind, actor_id, action, target, ip, user_agent, outcome, details, private_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		millis(e.Time), nullString(e.AccountID), e.ActorKind, e.ActorID, e.Action, e.Target, e.IP,
		e.UserAgent, e.Outcome, details, nullString(e.PrivateUserID))
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	if err != nil {
		return err
	}
	// The change this entry records reports its event in the same transaction (webhook_events.go).
	return tx.emitAudit(ctx, e)
}

// AuditLog lists an account's audit entries, newest first. A reader that sees everything (the account
// key) gets them all. Any other reader gets the entries that are not private, and the ones private to
// viewerUserID, which is "" for a caller that is no user. The filter is in the query, so paging counts
// only what the reader may see.
func (s *Store) AuditLog(ctx context.Context, accountID, viewerUserID string, seeAll bool, p Page) ([]*AuditEntry, string, error) {
	return s.AuditLogFiltered(ctx, accountID, viewerUserID, seeAll, AuditFilter{}, p)
}

// AuditFilter narrows an audit listing to one action, or to entries by one actor. Empty fields do not narrow.
type AuditFilter struct {
	Action  string
	ActorID string
}

// AuditLogFiltered is AuditLog narrowed by f. The visibility rule is the same: a private entry is listed
// only to the user it is private to, or to the caller who sees everything.
func (s *Store) AuditLogFiltered(ctx context.Context, accountID, viewerUserID string, seeAll bool, f AuditFilter, p Page) ([]*AuditEntry, string, error) {
	before := int64(1<<63 - 1)
	if p.Cursor != "" {
		parts, err := decodeCursor(p.Cursor, 1)
		if err != nil {
			return nil, "", err
		}
		if before, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
			return nil, "", ErrBadCursor
		}
	}
	visible := "1 = 1"
	args := []any{accountID, before}
	if !seeAll {
		visible = "(private_user_id IS NULL OR private_user_id = ?)"
		args = append(args, viewerUserID)
	}
	if f.Action != "" {
		visible += " AND action = ?"
		args = append(args, f.Action)
	}
	if f.ActorID != "" {
		visible += " AND actor_id = ?"
		args = append(args, f.ActorID)
	}
	args = append(args, p.Limit+1)
	rows, err := s.r.QueryContext(ctx, `SELECT id, time, account_id, actor_kind, actor_id, action,
		target, ip, user_agent, outcome, details FROM audit_log
		WHERE account_id = ? AND id < ? AND `+visible+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var t int64
		var acc, details sql.NullString
		if err := rows.Scan(&e.ID, &t, &acc, &e.ActorKind, &e.ActorID, &e.Action, &e.Target, &e.IP,
			&e.UserAgent, &e.Outcome, &details); err != nil {
			return nil, "", err
		}
		e.Time = fromMillis(t)
		e.AccountID = acc.String
		if details.Valid {
			if err := json.Unmarshal([]byte(details.String), &e.Details); err != nil {
				return nil, "", err
			}
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > p.Limit {
		out = out[:p.Limit]
		next = encodeCursor(strconv.FormatInt(out[len(out)-1].ID, 10))
	}
	return out, next, nil
}

// AdminAuditQuery narrows the listing of `selfcloud admin audit`, which reads every account and the installation. Empty
// fields do not narrow. Every value is bound as a parameter; only the placeholders are built.
type AdminAuditQuery struct {
	AccountID    string    // one account's entries; "" for every entry, the installation-wide ones included
	UserIDs      []string  // entries whose actor or target is one of these user IDs
	ActionPrefix string    // entries whose action starts with this text, matched literally
	Since        time.Time // entries at or after this time; the zero time does not narrow
	Limit        int       // the most entries to return
}

// AdminAuditEntries lists the audit entries that q narrows to, newest first, at most q.Limit of them.
func (s *Store) AdminAuditEntries(ctx context.Context, q AdminAuditQuery) ([]*AuditEntry, error) {
	where := []string{"1 = 1"}
	var args []any
	if q.AccountID != "" {
		where = append(where, "account_id = ?")
		args = append(args, q.AccountID)
	}
	if len(q.UserIDs) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(q.UserIDs)), ",")
		where = append(where, "(actor_id IN ("+marks+") OR target IN ("+marks+"))")
		for _, u := range q.UserIDs {
			args = append(args, u)
		}
		for _, u := range q.UserIDs {
			args = append(args, u)
		}
	}
	if q.ActionPrefix != "" {
		where = append(where, `action LIKE ? ESCAPE '\'`)
		args = append(args, likeEscaper.Replace(q.ActionPrefix)+"%")
	}
	if !q.Since.IsZero() {
		where = append(where, "time >= ?")
		args = append(args, millis(q.Since))
	}
	args = append(args, q.Limit)
	rows, err := s.r.QueryContext(ctx, `SELECT id, time, account_id, actor_kind, actor_id, action,
		target, ip, user_agent, outcome, details, private_user_id FROM audit_log
		WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var t int64
		var acc, details, private sql.NullString
		if err := rows.Scan(&e.ID, &t, &acc, &e.ActorKind, &e.ActorID, &e.Action, &e.Target, &e.IP,
			&e.UserAgent, &e.Outcome, &details, &private); err != nil {
			return nil, err
		}
		e.Time = fromMillis(t)
		e.AccountID = acc.String
		e.PrivateUserID = private.String
		if details.Valid {
			if err := json.Unmarshal([]byte(details.String), &e.Details); err != nil {
				return nil, err
			}
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// PruneAccountAudit removes up to batch audit entries of one account and reports how many it removed. The deletion of
// an account calls it in batches, each its own write, before the account's own delete, so that the cascade has little
// left to do under the writer (decision 48).
func (s *Store) PruneAccountAudit(ctx context.Context, accountID string, batch int) (int64, error) {
	var n int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM audit_log WHERE id IN (
			SELECT id FROM audit_log WHERE account_id = ? LIMIT ?)`, accountID, batch)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// PruneAudit removes up to batch audit entries made before the given time, installation-wide ones included, and
// reports how many it removed. Each call is one write, so a backlog is removed over several calls (decision 48).
func (s *Store) PruneAudit(ctx context.Context, before time.Time, batch int) (int64, error) {
	var n int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM audit_log WHERE id IN (
			SELECT id FROM audit_log WHERE time < ? LIMIT ?)`, millis(before), batch)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}
