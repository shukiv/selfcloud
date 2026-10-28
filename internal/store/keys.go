package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// APIKey is a server-to-server credential. Only its hash is stored.
type APIKey struct {
	ID         string
	AccountID  string // "" for an installation admin key
	Name       string
	Prefix     string
	Hash       []byte
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

const keyCols = "id, account_id, name, prefix, hash, created_at, expires_at, revoked_at, last_used_at"

func scanKey(row interface{ Scan(...any) error }) (*APIKey, error) {
	var k APIKey
	var acc sql.NullString
	var created int64
	var expires, revoked, used sql.NullInt64
	err := row.Scan(&k.ID, &acc, &k.Name, &k.Prefix, &k.Hash, &created, &expires, &revoked, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.AccountID = acc.String
	k.CreatedAt = fromMillis(created)
	k.ExpiresAt = fromNullMillis(expires)
	k.RevokedAt = fromNullMillis(revoked)
	k.LastUsedAt = fromNullMillis(used)
	return &k, nil
}

// APIKeyByHash returns the key with the given hash, revoked or not.
func (s *Store) APIKeyByHash(ctx context.Context, hash []byte) (*APIKey, error) {
	return scanKey(s.r.QueryRowContext(ctx, "SELECT "+keyCols+" FROM api_keys WHERE hash = ?", hash))
}

// APIKeys lists an account's keys, or the installation admin keys when accountID is "",
// oldest first.
func (s *Store) APIKeys(ctx context.Context, accountID string) ([]*APIKey, error) {
	query := "SELECT " + keyCols + " FROM api_keys WHERE account_id = ? ORDER BY created_at, id"
	args := []any{accountID}
	if accountID == "" {
		query = "SELECT " + keyCols + " FROM api_keys WHERE account_id IS NULL ORDER BY created_at, id"
		args = nil
	}
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// InsertAPIKey adds k.
func (tx *Tx) InsertAPIKey(ctx context.Context, k *APIKey) error {
	_, err := tx.q.ExecContext(ctx, "INSERT INTO api_keys ("+keyCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		k.ID, nullString(k.AccountID), k.Name, k.Prefix, k.Hash, millis(k.CreatedAt),
		nullMillis(k.ExpiresAt), nullMillis(k.RevokedAt), nullMillis(k.LastUsedAt))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// RevokeAPIKey revokes the key with the given ID that belongs to accountID ("" for admin
// keys). Revoking a revoked key keeps its first revocation time. It returns ErrNotFound if no
// such key belongs there.
func (tx *Tx) RevokeAPIKey(ctx context.Context, accountID, id string, at time.Time) error {
	query := "UPDATE api_keys SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND account_id = ?"
	args := []any{millis(at), id, accountID}
	if accountID == "" {
		query = "UPDATE api_keys SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND account_id IS NULL"
		args = args[:2]
	}
	return rowsAffected(tx.q.ExecContext(ctx, query, args...))
}

// TouchAPIKey records that the key was used.
func (s *Store) TouchAPIKey(ctx context.Context, id string, at time.Time) error {
	return s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, "UPDATE api_keys SET last_used_at = ? WHERE id = ?", millis(at), id)
		return err
	})
}
