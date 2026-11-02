// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Playlist is a user's named, ordered list of files (decision 58). Its entries are read with PlaylistEntries.
type Playlist struct {
	ID         string
	AccountID  string
	UserID     string
	Name       string
	EntryCount int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PlaylistEntry is one place in a playlist: a node's ID at a position. It does not say whether the node is still
// there or still reached; the API decides that at each read.
type PlaylistEntry struct {
	ID         string
	PlaylistID string
	Position   int
	NodeID     string
	AddedAt    time.Time
}

// InsertPlaylist adds an empty playlist.
func (tx *Tx) InsertPlaylist(ctx context.Context, p Playlist, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO playlists (id, account_id, user_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, p.ID, p.AccountID, p.UserID, p.Name, millis(at), millis(at))
	return err
}

// CountPlaylists returns how many playlists userID holds.
func (tx *Tx) CountPlaylists(ctx context.Context, userID string) (int, error) {
	var n int
	err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM playlists WHERE user_id = ?", userID).Scan(&n)
	return n, err
}

// RenamePlaylist sets the name of a playlist of userID and touches it. It reports whether the playlist was found.
func (tx *Tx) RenamePlaylist(ctx context.Context, userID, id, name string, at time.Time) (bool, error) {
	res, err := tx.q.ExecContext(ctx, `UPDATE playlists SET name = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		name, millis(at), id, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeletePlaylist removes a playlist of userID, with its entries. It reports whether one was found.
func (tx *Tx) DeletePlaylist(ctx context.Context, userID, id string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM playlists WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// TouchPlaylist sets updated_at of a playlist.
func (tx *Tx) TouchPlaylist(ctx context.Context, id string, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, "UPDATE playlists SET updated_at = ? WHERE id = ?", millis(at), id)
	return err
}

// PlaylistOf reads a playlist of userID, from a read or a write. found is false for a playlist of another user as for
// one that does not exist.
func PlaylistOf(ctx context.Context, q Querier, userID, id string) (Playlist, bool, error) {
	var p Playlist
	var created, updated int64
	err := q.QueryRowContext(ctx, `SELECT p.id, p.account_id, p.user_id, p.name, p.created_at, p.updated_at,
		(SELECT COUNT(*) FROM playlist_entries e WHERE e.playlist_id = p.id)
		FROM playlists p WHERE p.id = ? AND p.user_id = ?`, id, userID).
		Scan(&p.ID, &p.AccountID, &p.UserID, &p.Name, &created, &updated, &p.EntryCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, false, nil
	}
	if err != nil {
		return Playlist{}, false, err
	}
	p.CreatedAt, p.UpdatedAt = fromMillis(created), fromMillis(updated)
	return p, true, nil
}

// PlaylistsOf lists the playlists of userID, oldest first. A user holds at most a bounded number, so the list is whole.
func (s *Store) PlaylistsOf(ctx context.Context, userID string) ([]Playlist, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT p.id, p.account_id, p.user_id, p.name, p.created_at, p.updated_at,
		(SELECT COUNT(*) FROM playlist_entries e WHERE e.playlist_id = p.id)
		FROM playlists p WHERE p.user_id = ? ORDER BY p.created_at, p.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		var p Playlist
		var created, updated int64
		if err := rows.Scan(&p.ID, &p.AccountID, &p.UserID, &p.Name, &created, &updated, &p.EntryCount); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = fromMillis(created), fromMillis(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlaylistEntries reads the entries of a playlist in order. The caller has checked that the playlist is its user's.
func PlaylistEntries(ctx context.Context, q Querier, playlistID string) ([]PlaylistEntry, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, position, node_id, added_at FROM playlist_entries
		WHERE playlist_id = ? ORDER BY position, id`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlaylistEntry
	for rows.Next() {
		e := PlaylistEntry{PlaylistID: playlistID}
		var added int64
		if err := rows.Scan(&e.ID, &e.Position, &e.NodeID, &added); err != nil {
			return nil, err
		}
		e.AddedAt = fromMillis(added)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AppendPlaylistEntry adds a node at the end of a playlist.
func (tx *Tx) AppendPlaylistEntry(ctx context.Context, e PlaylistEntry, at time.Time) error {
	_, err := tx.q.ExecContext(ctx, `INSERT INTO playlist_entries (id, playlist_id, position, node_id, added_at)
		VALUES (?, ?, (SELECT COALESCE(MAX(position) + 1, 0) FROM playlist_entries WHERE playlist_id = ?), ?, ?)`,
		e.ID, e.PlaylistID, e.PlaylistID, e.NodeID, millis(at))
	return err
}

// DeletePlaylistEntry removes one entry of a playlist. It reports whether the entry was there.
func (tx *Tx) DeletePlaylistEntry(ctx context.Context, playlistID, entryID string) (bool, error) {
	res, err := tx.q.ExecContext(ctx, "DELETE FROM playlist_entries WHERE playlist_id = ? AND id = ?", playlistID, entryID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ReplacePlaylistEntries sets the entries of a playlist to the given list, in order. The caller gives every entry its
// ID, so entries kept from before keep theirs.
func (tx *Tx) ReplacePlaylistEntries(ctx context.Context, playlistID string, entries []PlaylistEntry, at time.Time) error {
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM playlist_entries WHERE playlist_id = ?", playlistID); err != nil {
		return err
	}
	for i, e := range entries {
		_, err := tx.q.ExecContext(ctx, `INSERT INTO playlist_entries (id, playlist_id, position, node_id, added_at)
			VALUES (?, ?, ?, ?, ?)`, e.ID, playlistID, i, e.NodeID, millis(at))
		if err != nil {
			return err
		}
	}
	return nil
}

// SetPlaylistPositions gives each entry of a playlist the position it is listed with, in the order given.
func (tx *Tx) SetPlaylistPositions(ctx context.Context, playlistID string, entryIDs []string) error {
	for i, id := range entryIDs {
		if _, err := tx.q.ExecContext(ctx, "UPDATE playlist_entries SET position = ? WHERE playlist_id = ? AND id = ?",
			i, playlistID, id); err != nil {
			return err
		}
	}
	return nil
}

// Querier is the statement runner of the transaction, for the reads a write makes inside itself: the playlist's own
// rows, and the reach of its entries.
func (tx *Tx) Querier() Querier { return tx.q }
