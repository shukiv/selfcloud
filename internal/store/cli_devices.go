// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CLIRequest is a pending device sign-in (decision 125). Only the hashes of the device code, of the user code's first
// half (the selector) and of the whole user code are stored. Approval fills AccountID, UserID and ApprovedAt.
type CLIRequest struct {
	ID             string
	DeviceCodeHash []byte
	SelectorHash   []byte
	CodeHash       []byte
	ClientName     string
	ClientIP       string
	UserAgent      string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	Interval       time.Duration
	LastPollAt     time.Time
	Attempts       int
	ApprovedAt     *time.Time
	AccountID      string
	UserID         string
}

// CLIDevice is a device a person signed in at a terminal. Its token names ID; the token itself is never stored.
type CLIDevice struct {
	ID         string
	AccountID  string
	UserID     string
	ClientName string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

// InsertCLIRequest adds r. A selector or device code hash that is already held is ErrConflict, so the caller can draw
// another code.
func (tx *Tx) InsertCLIRequest(ctx context.Context, r *CLIRequest) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO cli_device_requests
		(id, device_code_hash, selector_hash, code_hash, client_name, client_ip, user_agent, created_at, expires_at,
		 interval_ms, last_poll_at, attempts, approved_at, account_id, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.DeviceCodeHash, r.SelectorHash, r.CodeHash, r.ClientName, r.ClientIP, r.UserAgent,
		millis(r.CreatedAt), millis(r.ExpiresAt), r.Interval.Milliseconds(), millis(r.LastPollAt), r.Attempts,
		nullMillis(r.ApprovedAt), nullString(r.AccountID), nullString(r.UserID))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// cliRequestCols lists the columns cliRequestRow reads, in order.
const cliRequestCols = `id, device_code_hash, selector_hash, code_hash, client_name, client_ip, user_agent, created_at,
	expires_at, interval_ms, last_poll_at, attempts, approved_at, account_id, user_id`

// cliRequestRow scans one request row.
func cliRequestRow(row interface{ Scan(...any) error }) (*CLIRequest, error) {
	var (
		r                                         CLIRequest
		created, expires, interval, lastPoll, att int64
		approved                                  sql.NullInt64
		account, user                             sql.NullString
	)
	err := row.Scan(&r.ID, &r.DeviceCodeHash, &r.SelectorHash, &r.CodeHash, &r.ClientName, &r.ClientIP, &r.UserAgent,
		&created, &expires, &interval, &lastPoll, &att, &approved, &account, &user)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt = fromMillis(created)
	r.ExpiresAt = fromMillis(expires)
	r.Interval = time.Duration(interval) * time.Millisecond
	r.LastPollAt = fromMillis(lastPoll)
	r.Attempts = int(att)
	r.ApprovedAt = fromNullMillis(approved)
	r.AccountID = account.String
	r.UserID = user.String
	return &r, nil
}

// CLIRequestByDeviceHash returns the request whose device code has this hash, or ErrNotFound.
func (tx *Tx) CLIRequestByDeviceHash(ctx context.Context, hash []byte) (*CLIRequest, error) {
	return cliRequestRow(tx.q.QueryRowContext(ctx, `SELECT `+cliRequestCols+` FROM cli_device_requests WHERE device_code_hash = ?`, hash))
}

// CLIRequestBySelector returns the request whose user code's first half has this hash, or ErrNotFound.
func (tx *Tx) CLIRequestBySelector(ctx context.Context, hash []byte) (*CLIRequest, error) {
	return cliRequestRow(tx.q.QueryRowContext(ctx, `SELECT `+cliRequestCols+` FROM cli_device_requests WHERE selector_hash = ?`, hash))
}

// UpdateCLIRequest writes the mutable fields of r: the poll's interval and time, the attempts, and the approval.
func (tx *Tx) UpdateCLIRequest(ctx context.Context, r *CLIRequest) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE cli_device_requests
		SET interval_ms = ?, last_poll_at = ?, attempts = ?, approved_at = ?, account_id = ?, user_id = ?
		WHERE id = ?`,
		r.Interval.Milliseconds(), millis(r.LastPollAt), r.Attempts, nullMillis(r.ApprovedAt),
		nullString(r.AccountID), nullString(r.UserID), r.ID))
}

// DeleteCLIRequest removes a request. A request that is already gone is ErrNotFound.
func (tx *Tx) DeleteCLIRequest(ctx context.Context, id string) error {
	return rowsAffected(tx.q.ExecContext(ctx, `DELETE FROM cli_device_requests WHERE id = ?`, id))
}

// InsertCLIDevice adds d.
func (tx *Tx) InsertCLIDevice(ctx context.Context, d *CLIDevice) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO cli_devices (id, account_id, user_id, client_name, created_at, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.AccountID, d.UserID, d.ClientName, millis(d.CreatedAt), millis(d.ExpiresAt), nullMillis(d.RevokedAt))
	return err
}

// RevokeCLIDevice ends the device id of user userID in account accountID at at. A device that is not theirs, or is
// already revoked, is ErrNotFound.
func (tx *Tx) RevokeCLIDevice(ctx context.Context, accountID, userID, id string, at time.Time) error {
	return rowsAffected(tx.q.ExecContext(ctx, `UPDATE cli_devices SET revoked_at = ?
		WHERE id = ? AND account_id = ? AND user_id = ? AND revoked_at IS NULL`,
		millis(at), id, accountID, userID))
}

// CLIDevicesOf lists the devices of a user that still work at now: not revoked, not expired, and issued after the
// account's and the user's cut-offs. It is the list Manage shows. The check mirrors the API's callerLive.
func (s *Store) CLIDevicesOf(ctx context.Context, accountID, userID string, now time.Time) ([]*CLIDevice, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT d.id, d.account_id, d.user_id, d.client_name, d.created_at, d.expires_at
		FROM cli_devices d
		JOIN accounts a ON a.id = d.account_id
		JOIN users u ON u.id = d.user_id AND u.account_id = d.account_id
		WHERE d.account_id = ? AND d.user_id = ? AND d.revoked_at IS NULL AND d.expires_at > ?
		  AND d.created_at > a.tokens_not_before AND d.created_at > u.tokens_not_before
		ORDER BY d.created_at DESC`, accountID, userID, millis(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CLIDevice
	for rows.Next() {
		var d CLIDevice
		var created, expires int64
		if err := rows.Scan(&d.ID, &d.AccountID, &d.UserID, &d.ClientName, &created, &expires); err != nil {
			return nil, err
		}
		d.CreatedAt = fromMillis(created)
		d.ExpiresAt = fromMillis(expires)
		out = append(out, &d)
	}
	return out, rows.Err()
}

// DeviceAccess is the state a device token is checked against, in one query (design-cli 3.3). It is Delegation, with the
// device's row filled in: DeviceFound (the row exists, in this account, and belongs to this user), its revocation and its
// expiry. The account and user fields are read as Delegation reads them. It returns ErrNotFound when the account does not
// exist.
func (s *Store) DeviceAccess(ctx context.Context, accountID, deviceID, userID string) (*Delegation, error) {
	var (
		d                       Delegation
		tnb                     int64
		devAccount, devUser     sql.NullString
		devRevoked, devExpires  sql.NullInt64
		role, userStatus        sql.NullString
		userTnb, userMustChange sql.NullInt64
	)
	err := s.r.QueryRowContext(ctx, `SELECT a.status, a.tokens_not_before,
			dv.account_id, dv.user_id, dv.revoked_at, dv.expires_at,
			u.role, u.status, u.tokens_not_before, u.must_change_password
		FROM accounts a
		LEFT JOIN cli_devices dv ON dv.id = ? AND dv.account_id = a.id AND dv.user_id = ?
		LEFT JOIN users u ON u.id = ? AND u.account_id = a.id
		WHERE a.id = ?`, deviceID, userID, userID, accountID).Scan(&d.AccountStatus, &tnb,
		&devAccount, &devUser, &devRevoked, &devExpires, &role, &userStatus, &userTnb, &userMustChange)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.TokensNotBefore = fromMillis(tnb)
	d.DeviceFound = devAccount.Valid && devUser.Valid
	d.DeviceRevokedAt = fromNullMillis(devRevoked)
	if devExpires.Valid {
		d.DeviceExpiresAt = fromMillis(devExpires.Int64)
	}
	d.UserFound = role.Valid
	d.UserRole = role.String
	d.UserStatus = userStatus.String
	if userTnb.Valid {
		d.UserTokensNotBefore = fromMillis(userTnb.Int64)
	}
	d.UserMustChangePassword = userMustChange.Valid && userMustChange.Int64 == 1
	return &d, nil
}

// DeleteSpentCLIRequests removes the requests that expired before now. A request is never reused, so it can go at once.
func (tx *Tx) DeleteSpentCLIRequests(ctx context.Context, now time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM cli_device_requests WHERE expires_at <= ?`, millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteEndedCLIDevices removes the devices whose token has expired. An expired token is refused before any row is
// read, so the row is no longer needed.
func (tx *Tx) DeleteEndedCLIDevices(ctx context.Context, now time.Time) (int64, error) {
	res, err := tx.q.ExecContext(ctx, `DELETE FROM cli_devices WHERE expires_at <= ?`, millis(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
