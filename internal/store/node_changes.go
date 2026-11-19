// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// NodeChange is one row of the changes journal (migration 0062, decision 235): a write that changed a node, the folder
// it landed in, and whether a directory came back from the trash and must be listed again.
type NodeChange struct {
	Seq       int64
	AccountID string
	FolderID  string
	NodeID    string
	Relist    bool
	At        time.Time
}

func scanNodeChange(row interface{ Scan(...any) error }) (NodeChange, error) {
	var c NodeChange
	var relist int
	var at int64
	if err := row.Scan(&c.Seq, &c.AccountID, &c.FolderID, &c.NodeID, &relist, &at); err != nil {
		return NodeChange{}, err
	}
	c.Relist = relist != 0
	c.At = time.UnixMilli(at)
	return c, nil
}

// NodeChangesAfter reads the journal after seq, oldest first, up to limit. With a folder it reads that space's feed;
// with folder "" it reads the whole account's feed, skipping the rows of staged folders, which no client may see until
// they are published. A folder that is gone (its rows left by a purge or a delete) is still read, so the account feed
// reports the removal.
func (s *Store) NodeChangesAfter(ctx context.Context, accountID, folderID string, after int64, limit int) ([]NodeChange, error) {
	var rows *sql.Rows
	var err error
	if folderID != "" {
		rows, err = s.r.QueryContext(ctx, `SELECT seq, account_id, folder_id, node_id, relist, at FROM node_changes
			WHERE account_id = ? AND folder_id = ? AND seq > ? ORDER BY seq LIMIT ?`, accountID, folderID, after, limit)
	} else {
		rows, err = s.r.QueryContext(ctx, `SELECT c.seq, c.account_id, c.folder_id, c.node_id, c.relist, c.at
			FROM node_changes c LEFT JOIN folders f ON f.id = c.folder_id
			WHERE c.account_id = ? AND c.seq > ? AND (f.id IS NULL OR f.staged = 0) ORDER BY c.seq LIMIT ?`,
			accountID, after, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeChange{}
	for rows.Next() {
		c, err := scanNodeChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NodeChangesMaxSeq is the journal's head: the seq a starting cursor names. Zero when the journal is empty.
func (s *Store) NodeChangesMaxSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.r.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM node_changes").Scan(&seq)
	return seq, err
}

// NodeChangesMark is the highest seq the sweep has pruned. A cursor at or below it can no longer be served.
func (s *Store) NodeChangesMark(ctx context.Context) (int64, error) {
	var mark int64
	err := s.r.QueryRowContext(ctx, "SELECT pruned_through FROM node_changes_mark WHERE id = 1").Scan(&mark)
	return mark, err
}

// PruneNodeChanges removes up to batch journal rows older than before, and rows whose account is gone, oldest first.
// The delete and the mark's rise to the highest removed seq happen in one write, so a cursor is never served past a
// mark that has not yet taken its rows. The mark never lowers. It returns the number of rows removed.
func (s *Store) PruneNodeChanges(ctx context.Context, before time.Time, batch int) (int64, error) {
	var n int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT c.seq FROM node_changes c "+
			"WHERE c.at < ? OR NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id = c.account_id) "+
			"ORDER BY c.seq LIMIT ?", millis(before), batch)
		if err != nil {
			return err
		}
		var seqs []int64
		for rows.Next() {
			var seq int64
			if err := rows.Scan(&seq); err != nil {
				rows.Close()
				return err
			}
			seqs = append(seqs, seq)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(seqs) == 0 {
			return nil
		}
		marks := make([]string, len(seqs))
		args := make([]any, len(seqs))
		for i, seq := range seqs {
			marks[i] = "?"
			args[i] = seq
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM node_changes WHERE seq IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return err
		}
		if n, err = res.RowsAffected(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE node_changes_mark SET pruned_through = MAX(pruned_through, ?) WHERE id = 1",
			seqs[len(seqs)-1])
		return err
	})
	return n, err
}

// WatchWrites returns a channel signalled after each write commits, and a function that unsubscribes. A long poll waits
// on it to learn that the journal may have grown. The channel holds one signal: a write that lands while the watcher is
// reading is not lost, and several writes coalesce into one wake.
func (s *Store) WatchWrites() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.writeMu.Lock()
	if s.writeSubs == nil {
		s.writeSubs = make(map[chan struct{}]struct{})
	}
	s.writeSubs[ch] = struct{}{}
	s.writeMu.Unlock()
	return ch, func() {
		s.writeMu.Lock()
		delete(s.writeSubs, ch)
		s.writeMu.Unlock()
	}
}

// notifyWrites signals every watcher after a commit. It never blocks: a full channel already holds a wake.
func (s *Store) notifyWrites() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for ch := range s.writeSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
