// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import "context"

// The settings table (migration 0001) holds the installation-wide settings an admin changed through the settings API,
// one row per key, each value as the text the config file would hold (decision 139). A key with no row takes the file's
// value or its default.

// Settings returns every row of the settings table, by key.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	return settingsFrom(ctx, s.r)
}

// Settings returns every row of the settings table, by key, as the transaction sees it.
func (tx *Tx) Settings(ctx context.Context) (map[string]string, error) {
	return settingsFrom(ctx, tx.q)
}

func settingsFrom(ctx context.Context, q querier) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSetting stores value under key, replacing the row the key has.
func (tx *Tx) SetSetting(ctx context.Context, key, value string) error {
	_, err := tx.q.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// DeleteSetting removes the row of key. Removing a row that is not there is not an error.
func (tx *Tx) DeleteSetting(ctx context.Context, key string) error {
	_, err := tx.q.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key)
	return err
}
