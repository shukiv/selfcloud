// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// DirectoryEntry is one answer of a directory search: an active user, or a group, of one account.
type DirectoryEntry struct {
	Kind  string // SubjectUser or SubjectGroup
	ID    string
	Name  string
	Email string // a user's email; empty for a group
}

// likeEscaper makes a query match literally inside a LIKE pattern: the escape character and both
// wildcards are escaped. Every LIKE in this file uses ESCAPE '\'.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// SearchUsers returns up to limit active users of accountID whose email local part starts with q, or
// whose display name contains q. q is matched case-insensitively for ASCII; the caller has already
// trimmed it. A pending or disabled user is never returned.
func (s *Store) SearchUsers(ctx context.Context, accountID, q string, limit int) ([]DirectoryEntry, error) {
	esc := likeEscaper.Replace(strings.ToLower(q))
	rows, err := s.r.QueryContext(ctx, `SELECT id, email, display_name FROM users
		WHERE account_id = ? AND status = 'active' AND (
			substr(email_key, 1, instr(email_key, '@') - 1) LIKE ? || '%' ESCAPE '\'
			OR lower(display_name) LIKE '%' || ? || '%' ESCAPE '\')
		ORDER BY lower(display_name), email_key LIMIT ?`,
		accountID, esc, esc, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DirectoryEntry
	for rows.Next() {
		var e DirectoryEntry
		var display string
		if err := rows.Scan(&e.ID, &e.Email, &display); err != nil {
			return nil, err
		}
		e.Kind = SubjectUser
		e.Name = display
		if e.Name == "" {
			e.Name = e.Email
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SearchGroups returns up to limit groups of accountID whose name contains q. The match uses the same
// case folding as a group's name key, so it is case-insensitive for every script.
func (s *Store) SearchGroups(ctx context.Context, accountID, q string, limit int) ([]DirectoryEntry, error) {
	key := norm.NFC.String(cases.Fold().String(q))
	rows, err := s.r.QueryContext(ctx, `SELECT id, name FROM groups
		WHERE account_id = ? AND name_key LIKE '%' || ? || '%' ESCAPE '\'
		ORDER BY name_key LIMIT ?`,
		accountID, likeEscaper.Replace(key), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DirectoryEntry
	for rows.Next() {
		var e DirectoryEntry
		if err := rows.Scan(&e.ID, &e.Name); err != nil {
			return nil, err
		}
		e.Kind = SubjectGroup
		out = append(out, e)
	}
	return out, rows.Err()
}
