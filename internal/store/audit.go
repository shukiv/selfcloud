package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
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
	res, err := tx.q.ExecContext(ctx, `INSERT INTO audit_log
		(time, account_id, actor_kind, actor_id, action, target, ip, user_agent, outcome, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		millis(e.Time), nullString(e.AccountID), e.ActorKind, e.ActorID, e.Action, e.Target, e.IP,
		e.UserAgent, e.Outcome, details)
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// AuditLog lists an account's audit entries, newest first.
func (s *Store) AuditLog(ctx context.Context, accountID string, p Page) ([]*AuditEntry, string, error) {
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
	rows, err := s.r.QueryContext(ctx, `SELECT id, time, account_id, actor_kind, actor_id, action,
		target, ip, user_agent, outcome, details FROM audit_log
		WHERE account_id = ? AND id < ? ORDER BY id DESC LIMIT ?`, accountID, before, p.Limit+1)
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
