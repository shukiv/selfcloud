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
// secret was issued: a visit recorded before it no longer counts, and a visitor's credential issued
// before it no longer works. PasswordHash is the argon2id hash of an anyone link's password, or "".
type ShareLink struct {
	ID            string
	AccountID     string
	NodeID        string
	Audience      string
	Role          string // viewer, commenter, contributor or editor; "" for a drop link
	UploadOnly    bool   // a drop link: uploads into NodeID and nothing else (slice o3)
	SecretHash    []byte
	PasswordHash  string
	ExpiresAt     *time.Time
	CredentialsAt time.Time
	CreatedBy     string
	CreatedKind   string
	CreatedAt     time.Time
	UpdatedBy     string
	UpdatedKind   string
	UpdatedAt     time.Time
	// The fields below belong to drop links. Title and Message are shown to the visitor. AskName makes the
	// visitor give a name and email. Notify mails the creator the files that arrive: UnreportedFiles counts the
	// files not yet mailed, and NotifiedAt is when the last mail went (nil before the first).
	Title           string
	Message         string
	AskName         bool
	Notify          bool
	UnreportedFiles int
	NotifiedAt      *time.Time
}

// ShareLinkView is a link as a listing shows it: the link, how many members hold a live visit to it, and
// whether its team space's policy pauses it.
type ShareLinkView struct {
	ShareLink
	Visitors int
	Paused   bool
}

const shareLinkCols = `sl.id, sl.account_id, sl.node_id, sl.audience, sl.role, sl.secret_hash, sl.password_hash,
	sl.expires_at, sl.credentials_at, sl.created_by, sl.created_kind, sl.created_at, sl.updated_by, sl.updated_kind,
	sl.updated_at, sl.upload_only, sl.title, sl.message, sl.ask_name, sl.notify, sl.unreported_files, sl.notified_at`

// linkRow holds the columns of shareLinkCols as the database gives them, before they become a ShareLink.
type linkRow struct {
	role, password       sql.NullString
	expires, notified    sql.NullInt64
	credentials, created int64
	updated              int64
	uploadOnly, askName  int64
	notify, unreported   int64
}

// scanLinkRow reads the columns of shareLinkCols into l, through row. extra are destinations for the columns a
// query puts after them.
func scanLinkRow(row interface{ Scan(...any) error }, l *ShareLink, extra ...any) error {
	var r linkRow
	dest := []any{&l.ID, &l.AccountID, &l.NodeID, &l.Audience, &r.role, &l.SecretHash, &r.password, &r.expires,
		&r.credentials, &l.CreatedBy, &l.CreatedKind, &r.created, &l.UpdatedBy, &l.UpdatedKind, &r.updated,
		&r.uploadOnly, &l.Title, &l.Message, &r.askName, &r.notify, &r.unreported, &r.notified}
	err := row.Scan(append(dest, extra...)...)
	if err != nil {
		return err
	}
	l.Role = r.role.String
	l.UploadOnly = r.uploadOnly == 1
	l.PasswordHash = r.password.String
	l.ExpiresAt = fromNullMillis(r.expires)
	l.CredentialsAt = fromMillis(r.credentials)
	l.CreatedAt = fromMillis(r.created)
	l.UpdatedAt = fromMillis(r.updated)
	l.AskName = r.askName == 1
	l.Notify = r.notify == 1
	l.UnreportedFiles = int(r.unreported)
	l.NotifiedAt = fromNullMillis(r.notified)
	return nil
}

func scanShareLink(row interface{ Scan(...any) error }) (*ShareLink, error) {
	var l ShareLink
	err := scanLinkRow(row, &l)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
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
// access link and one drop link.
func (tx *Tx) InsertShareLink(ctx context.Context, l *ShareLink) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO share_links (id, account_id, node_id, audience, role, upload_only,
		secret_hash, password_hash, expires_at, credentials_at, created_by, created_kind, created_at, updated_by,
		updated_kind, updated_at, title, message, ask_name, notify, unreported_files, notified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL)`,
		l.ID, l.AccountID, l.NodeID, l.Audience, nullString(l.Role), boolInt(l.UploadOnly), l.SecretHash,
		nullString(l.PasswordHash), nullMillis(l.ExpiresAt), millis(l.CredentialsAt), l.CreatedBy, l.CreatedKind,
		millis(l.CreatedAt), l.UpdatedBy, l.UpdatedKind, millis(l.UpdatedAt), l.Title, l.Message,
		boolInt(l.AskName), boolInt(l.Notify))
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

// UpdateShareLink writes the mutable fields of l: its audience, role, expiry, password, secret and the
// last changer. A reset is an update with a new SecretHash and CredentialsAt.
func (tx *Tx) UpdateShareLink(ctx context.Context, l *ShareLink) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE share_links SET audience = ?, role = ?, expires_at = ?,
		secret_hash = ?, password_hash = ?, credentials_at = ?, updated_by = ?, updated_kind = ?, updated_at = ?,
		title = ?, message = ?, ask_name = ?, notify = ?
		WHERE account_id = ? AND id = ?`,
		l.Audience, nullString(l.Role), nullMillis(l.ExpiresAt), l.SecretHash, nullString(l.PasswordHash),
		millis(l.CredentialsAt), l.UpdatedBy, l.UpdatedKind, millis(l.UpdatedAt), l.Title, l.Message,
		boolInt(l.AskName), boolInt(l.Notify), l.AccountID, l.ID))
}

// DeleteShareLinkVisits removes the visits to a link. An anyone link has none, so a link that changes to
// that audience sheds the members it had as an organisation link.
func (tx *Tx) DeleteShareLinkVisits(ctx context.Context, linkID string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM share_link_visits WHERE link_id = ?", linkID)
	return err
}

// ShareLinkState is what a visitor's credential, or an open of a secret, needs to know about a link in one
// read: the link, its node's trash and position, its folder's policy and its account's state.
type ShareLinkState struct {
	Link          ShareLink
	FolderID      string // the folder the node is in
	NodeTrashed   bool   // the node itself is an explicit trash root
	NodeIsRoot    bool   // the node is a folder's root
	FolderKind    string // KindTeam or KindPersonal
	AllowLinks    bool   // the team space allows links
	MembersOnly   bool   // the team space admits links on its root only
	AccountStatus string
	PublicLinks   bool // the account's switch for links that anyone can open
}

const shareLinkStateSQL = `SELECT ` + shareLinkCols + `,
	ln.folder_id, ln.trashed_at IS NOT NULL, ln.parent_id IS NULL, lf.kind, lf.allow_links, lf.members_only,
	ac.status, ac.public_links
	FROM share_links sl JOIN nodes ln ON ln.id = sl.node_id JOIN folders lf ON lf.id = ln.folder_id
	JOIN accounts ac ON ac.id = sl.account_id`

func scanShareLinkState(row interface{ Scan(...any) error }) (*ShareLinkState, error) {
	var (
		st                                         ShareLinkState
		trashed, root, allow, members, publicLinks int64
	)
	err := scanLinkRow(row, &st.Link, &st.FolderID, &trashed, &root, &st.FolderKind, &allow, &members,
		&st.AccountStatus, &publicLinks)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st.NodeTrashed = trashed == 1
	st.NodeIsRoot = root == 1
	st.AllowLinks = allow == 1
	st.MembersOnly = members == 1
	st.PublicLinks = publicLinks == 1
	return &st, nil
}

// ShareLinkStateByID reads the state of the link with the given ID in accountID, or ErrNotFound.
func (s *Store) ShareLinkStateByID(ctx context.Context, accountID, id string) (*ShareLinkState, error) {
	return scanShareLinkState(s.r.QueryRowContext(ctx, shareLinkStateSQL+" WHERE sl.account_id = ? AND sl.id = ?",
		accountID, id))
}

// ShareLinkStateByID is Store.ShareLinkStateByID read inside the transaction.
func (tx *Tx) ShareLinkStateByID(ctx context.Context, accountID, id string) (*ShareLinkState, error) {
	return scanShareLinkState(tx.q.QueryRowContext(ctx, shareLinkStateSQL+" WHERE sl.account_id = ? AND sl.id = ?",
		accountID, id))
}

// ShareLinkStateBySecretHash reads the state of the link whose secret has the given hash, in any account, or
// ErrNotFound. The caller checks the account.
func (s *Store) ShareLinkStateBySecretHash(ctx context.Context, hash []byte) (*ShareLinkState, error) {
	return scanShareLinkState(s.r.QueryRowContext(ctx, shareLinkStateSQL+" WHERE sl.secret_hash = ?", hash))
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
			NOT ((sl.audience = 'organisation' OR ac.public_links = 1) AND `+linkPolicySQL+`)
		FROM chain c JOIN share_links sl ON sl.node_id = c.id AND sl.account_id = ?
		JOIN nodes ln ON ln.id = sl.node_id JOIN folders lf ON lf.id = ln.folder_id
		JOIN accounts ac ON ac.id = sl.account_id
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
		if err := scanLinkRow(rows, &v.ShareLink, &v.Visitors, &paused); err != nil {
			return nil, err
		}
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

// SharingPolicy is Store.SharingPolicy read inside the transaction.
func (tx *Tx) SharingPolicy(ctx context.Context, accountID string) (publicLinks bool, err error) {
	var on int64
	err = tx.q.QueryRowContext(ctx, "SELECT public_links FROM accounts WHERE id = ?", accountID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return on == 1, err
}

// CountAnyoneLinks counts the account's links that anyone can open and that have not ended at now, whether the
// switch is on or off. An expired link is left out, as a listing leaves it out.
func (s *Store) CountAnyoneLinks(ctx context.Context, accountID string, now time.Time) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM share_links WHERE account_id = ? AND audience = ?
		AND (expires_at IS NULL OR expires_at > ?)`, accountID, AudienceAnyone, millis(now)).Scan(&n)
	return n, err
}

// SetSharingPolicy stores the account's switch for links that anyone can open.
func (tx *Tx) SetSharingPolicy(ctx context.Context, accountID string, publicLinks bool, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, "UPDATE accounts SET public_links = ?, updated_at = ? WHERE id = ?",
		boolInt(publicLinks), millis(at), accountID))
}

// AddDropFile counts one file that arrived through a drop link, for the creator's next mail.
func (tx *Tx) AddDropFile(ctx context.Context, accountID, linkID string) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE share_links SET unreported_files = unreported_files + 1
		WHERE account_id = ? AND id = ? AND upload_only = 1`, accountID, linkID))
}

// DueDropNotices lists up to limit drop links whose creator is owed a mail at now: the link asks to notify, files
// have arrived that no mail reported, and the last mail went an hour ago or none did.
func (s *Store) DueDropNotices(ctx context.Context, now time.Time, limit int) ([]*ShareLink, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+shareLinkCols+` FROM share_links sl
		WHERE sl.upload_only = 1 AND sl.notify = 1 AND sl.unreported_files > 0
		AND (sl.notified_at IS NULL OR sl.notified_at <= ?)
		ORDER BY sl.id LIMIT ?`, millis(now.Add(-time.Hour)), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ShareLink
	for rows.Next() {
		l, err := scanShareLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ClaimDropNotice takes the files a drop link has not reported, and marks the mail as sent at now. It returns the
// count taken and the link's previous notified_at, so a mail that does not go can be given back with
// RestoreDropNotice. A link with nothing to report takes nothing.
func (tx *Tx) ClaimDropNotice(ctx context.Context, accountID, linkID string, now time.Time) (int, *time.Time, error) {
	var (
		count    int
		notified sql.NullInt64
	)
	err := tx.q.QueryRowContext(ctx, `SELECT unreported_files, notified_at FROM share_links
		WHERE account_id = ? AND id = ? AND upload_only = 1`, accountID, linkID).Scan(&count, &notified)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, ErrNotFound
	}
	if err != nil || count == 0 {
		return 0, nil, err
	}
	prev := fromNullMillis(notified)
	err = rowsAffected(tx.q.ExecContext(ctx, `UPDATE share_links SET unreported_files = 0, notified_at = ?
		WHERE account_id = ? AND id = ?`, millis(now), accountID, linkID))
	return count, prev, err
}

// RestoreDropNotice gives back what ClaimDropNotice took, when its mail did not go: the count is reported again.
// The mail time is set to notifiedAt, the failed try, so the link is due again an hour later, not on the next run.
// Files that arrived since the claim stay counted too.
func (tx *Tx) RestoreDropNotice(ctx context.Context, accountID, linkID string, count int, notifiedAt *time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE share_links SET unreported_files = unreported_files + ?,
		notified_at = ? WHERE account_id = ? AND id = ?`, count, nullMillis(notifiedAt), accountID, linkID))
}
