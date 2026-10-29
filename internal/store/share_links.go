// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Audiences of a share link. An organisation link is open to the members of its account; an anyone
// link to whoever holds its secret (slice o2).
const (
	AudienceOrganisation = "organisation"
	AudienceAnyone       = "anyone"
)

// Creator kinds, as the audit log names them.
const (
	CreatorUser       = "user"
	CreatorAccountKey = "account_key"
)

// ShareLink is a link to one file or directory. Only the hash of its secret is stored, and the
// secret itself is shown once, when the link is made or reset. CredentialsAt is the time the current
// secret was issued: a visit recorded before it no longer counts.
type ShareLink struct {
	ID            string
	AccountID     string
	NodeID        string
	Audience      string
	Role          string // viewer, commenter, contributor or editor
	SecretHash    []byte
	ExpiresAt     *time.Time
	CredentialsAt time.Time
	CreatedBy     string
	CreatedKind   string
	CreatedAt     time.Time
	UpdatedBy     string
	UpdatedKind   string
	UpdatedAt     time.Time
}

// ShareLinkView is a link as a listing shows it: the link, how many members hold a live visit to it, and
// whether its team space's policy pauses it.
type ShareLinkView struct {
	ShareLink
	Visitors int
	Paused   bool
}

const shareLinkCols = `sl.id, sl.account_id, sl.node_id, sl.audience, sl.role, sl.secret_hash, sl.expires_at,
	sl.credentials_at, sl.created_by, sl.created_kind, sl.created_at, sl.updated_by, sl.updated_kind, sl.updated_at`

func scanShareLink(row interface{ Scan(...any) error }) (*ShareLink, error) {
	var l ShareLink
	var role sql.NullString
	var expires sql.NullInt64
	var credentials, created, updated int64
	err := row.Scan(&l.ID, &l.AccountID, &l.NodeID, &l.Audience, &role, &l.SecretHash, &expires,
		&credentials, &l.CreatedBy, &l.CreatedKind, &created, &l.UpdatedBy, &l.UpdatedKind, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.Role = role.String
	l.ExpiresAt = fromNullMillis(expires)
	l.CredentialsAt = fromMillis(credentials)
	l.CreatedAt = fromMillis(created)
	l.UpdatedAt = fromMillis(updated)
	return &l, nil
}

// linkPolicySQL is the team-space policy an organisation link must pass: a personal folder always allows
// it; a team space must have allow_links on; and a team space with members_only admits a link on its
// root only. It reads the link's folder as lf and its node as ln.
const linkPolicySQL = `(lf.kind = 'personal' OR lf.allow_links = 1) AND (lf.members_only = 0 OR ln.parent_id IS NULL)`

// linkLiveSQL is the condition under which a visit to an organisation link still gives its role. It
// reads the link as sl, the member's visit as v, the link's node as ln and its folder as lf, and takes
// the time in Unix milliseconds. It is false once the link is expired, reset after the visit, on a
// trashed node, or paused by policy. A deleted link has no visit left, since visits go with it. A
// trashed ancestor of the link's node needs no check here: the access walk refuses every node below
// it, and a listing reaches only through that walk.
const linkLiveSQL = `sl.audience = 'organisation' AND sl.role IS NOT NULL
	AND (sl.expires_at IS NULL OR sl.expires_at > ?) AND v.visited_at > sl.credentials_at
	AND ln.trashed_at IS NULL AND ` + linkPolicySQL

// InsertShareLink adds l. A second link of the same kind on a node is ErrConflict: a node has one
// access link.
func (tx *Tx) InsertShareLink(ctx context.Context, l *ShareLink) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO share_links (id, account_id, node_id, audience, role, upload_only,
		secret_hash, expires_at, credentials_at, created_by, created_kind, created_at, updated_by, updated_kind, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.AccountID, l.NodeID, l.Audience, l.Role, l.SecretHash, nullMillis(l.ExpiresAt),
		millis(l.CredentialsAt), l.CreatedBy, l.CreatedKind, millis(l.CreatedAt), l.UpdatedBy, l.UpdatedKind,
		millis(l.UpdatedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func shareLinkByID(ctx context.Context, q querier, accountID, id string) (*ShareLink, error) {
	return scanShareLink(q.QueryRowContext(ctx, `SELECT `+shareLinkCols+` FROM share_links sl
		WHERE sl.account_id = ? AND sl.id = ?`, accountID, id))
}

// ShareLink returns the link with the given ID in accountID, or ErrNotFound.
func (s *Store) ShareLink(ctx context.Context, accountID, id string) (*ShareLink, error) {
	return shareLinkByID(ctx, s.r, accountID, id)
}

// ShareLink is Store.ShareLink read inside the transaction.
func (tx *Tx) ShareLink(ctx context.Context, accountID, id string) (*ShareLink, error) {
	return shareLinkByID(ctx, tx.q, accountID, id)
}

// ShareLinkBySecretHash returns the link whose secret has the given hash, in any account, or ErrNotFound.
// The caller checks the account.
func (s *Store) ShareLinkBySecretHash(ctx context.Context, hash []byte) (*ShareLink, error) {
	return scanShareLink(s.r.QueryRowContext(ctx, `SELECT `+shareLinkCols+` FROM share_links sl
		WHERE sl.secret_hash = ?`, hash))
}

// UpdateShareLink writes the mutable fields of l: its role, expiry, secret and the last changer. A reset
// is an update with a new SecretHash and CredentialsAt.
func (tx *Tx) UpdateShareLink(ctx context.Context, l *ShareLink) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE share_links SET role = ?, expires_at = ?, secret_hash = ?,
		credentials_at = ?, updated_by = ?, updated_kind = ?, updated_at = ? WHERE account_id = ? AND id = ?`,
		l.Role, nullMillis(l.ExpiresAt), l.SecretHash, millis(l.CredentialsAt), l.UpdatedBy, l.UpdatedKind,
		millis(l.UpdatedAt), l.AccountID, l.ID))
}

// DeleteShareLink removes a link and, with it, its visits. ErrNotFound when there is none.
func (tx *Tx) DeleteShareLink(ctx context.Context, accountID, id string) error {
	return rowsAffected(tx.q.ExecContext(ctx, "DELETE FROM share_links WHERE account_id = ? AND id = ?",
		accountID, id))
}

// ChainShareLinks lists the links on nodeID and on each of its ancestors, the node's own first, with
// their visitor counts and whether policy pauses them. Links that have expired at now are left out.
func (s *Store) ChainShareLinks(ctx context.Context, accountID, nodeID string, now time.Time) ([]*ShareLinkView, error) {
	rows, err := s.r.QueryContext(ctx, `WITH RECURSIVE chain(id, parent, depth) AS (
			SELECT id, parent_id, 0 FROM nodes WHERE account_id = ? AND id = ?
			UNION ALL
			SELECT p.id, p.parent_id, c.depth + 1 FROM nodes p JOIN chain c ON p.id = c.parent WHERE p.account_id = ?
		)
		SELECT `+shareLinkCols+`,
			(SELECT COUNT(*) FROM share_link_visits v WHERE v.link_id = sl.id AND v.visited_at > sl.credentials_at),
			NOT (`+linkPolicySQL+`)
		FROM chain c JOIN share_links sl ON sl.node_id = c.id AND sl.account_id = ?
		JOIN nodes ln ON ln.id = sl.node_id JOIN folders lf ON lf.id = ln.folder_id
		WHERE sl.expires_at IS NULL OR sl.expires_at > ?
		ORDER BY c.depth, sl.created_at, sl.id`,
		accountID, nodeID, accountID, accountID, millis(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ShareLinkView
	for rows.Next() {
		var v ShareLinkView
		var paused int64
		var expires sql.NullInt64
		var role sql.NullString
		var credentials, created, updated int64
		err := rows.Scan(&v.ID, &v.AccountID, &v.NodeID, &v.Audience, &role, &v.SecretHash, &expires,
			&credentials, &v.CreatedBy, &v.CreatedKind, &created, &v.UpdatedBy, &v.UpdatedKind, &updated,
			&v.Visitors, &paused)
		if err != nil {
			return nil, err
		}
		v.Role = role.String
		v.ExpiresAt = fromNullMillis(expires)
		v.CredentialsAt = fromMillis(credentials)
		v.CreatedAt = fromMillis(created)
		v.UpdatedAt = fromMillis(updated)
		v.Paused = paused == 1
		out = append(out, &v)
	}
	return out, rows.Err()
}

// ShareLinkVisitAt returns when userID last opened the organisation link, and whether it ever did.
func (tx *Tx) ShareLinkVisitAt(ctx context.Context, linkID, userID string) (time.Time, bool, error) {
	var ms int64
	err := tx.q.QueryRowContext(ctx, `SELECT visited_at FROM share_link_visits WHERE link_id = ? AND user_id = ?`,
		linkID, userID).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return fromMillis(ms), true, nil
}

// RecordShareLinkVisit records that userID opened the organisation link at the given time. A visit
// already there is moved to the time, which revives it after a reset.
func (tx *Tx) RecordShareLinkVisit(ctx context.Context, linkID, userID string, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO share_link_visits (link_id, user_id, visited_at) VALUES (?, ?, ?)
		ON CONFLICT (link_id, user_id) DO UPDATE SET visited_at = excluded.visited_at`,
		linkID, userID, millis(at))
	return err
}

// SharingPolicy returns the account's switch for links that anyone can open.
func (s *Store) SharingPolicy(ctx context.Context, accountID string) (publicLinks bool, err error) {
	var on int64
	err = s.r.QueryRowContext(ctx, "SELECT public_links FROM accounts WHERE id = ?", accountID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return on == 1, err
}

// SetSharingPolicy stores the account's switch for links that anyone can open.
func (tx *Tx) SetSharingPolicy(ctx context.Context, accountID string, publicLinks bool, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, "UPDATE accounts SET public_links = ?, updated_at = ? WHERE id = ?",
		boolInt(publicLinks), millis(at), accountID))
}
