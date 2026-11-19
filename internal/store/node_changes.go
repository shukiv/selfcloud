// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

// NodeChange is one row of the changes journal (migration 0062, decision 235): a write that changed a node, the folder
// it landed in, and whether a directory came back from the trash and must be listed again. Staged is set by the
// account feed alone: the row's folder is staged, so the reader skips the row until the publish, while the cursor
// still advances past it.
type NodeChange struct {
	Seq       int64
	AccountID string
	FolderID  string
	NodeID    string
	Relist    bool
	At        time.Time
	Staged    bool
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

func scanNodeChangeStaged(row interface{ Scan(...any) error }) (NodeChange, error) {
	var c NodeChange
	var relist, staged int
	var at int64
	if err := row.Scan(&c.Seq, &c.AccountID, &c.FolderID, &c.NodeID, &relist, &at, &staged); err != nil {
		return NodeChange{}, err
	}
	c.Relist = relist != 0
	c.At = time.UnixMilli(at)
	c.Staged = staged != 0
	return c, nil
}

// The feed reads stay on their indexes: the space feed on (folder_id, seq), the account feed on (account_id, seq).
// The account feed reads raw rows and marks the staged ones, so the reader can skip them while the cursor advances
// past them; a folder that is gone (its rows left by a purge or a delete) is not staged, so its removal is reported.
const nodeChangesSpaceSQL = `SELECT seq, account_id, folder_id, node_id, relist, at
	FROM node_changes INDEXED BY node_changes_folder
	WHERE account_id = ? AND folder_id = ? AND seq > ? ORDER BY seq LIMIT ?`

const nodeChangesAccountSQL = `SELECT c.seq, c.account_id, c.folder_id, c.node_id, c.relist, c.at,
		CASE WHEN f.staged = 1 THEN 1 ELSE 0 END
	FROM node_changes c INDEXED BY node_changes_account LEFT JOIN folders f ON f.id = c.folder_id
	WHERE c.account_id = ? AND c.seq > ? ORDER BY c.seq LIMIT ?`

// NodeChangesAfter reads the journal after seq, oldest first, up to limit. With a folder it reads that space's feed;
// with folder "" it reads the whole account's raw rows, marking the rows of staged folders, which no client may see
// until they are published.
func (s *Store) NodeChangesAfter(ctx context.Context, accountID, folderID string, after int64, limit int) ([]NodeChange, error) {
	return NodeChangesAfterIn(ctx, s.r, accountID, folderID, after, limit)
}

// NodeChangesAfterIn is NodeChangesAfter on the querier of one snapshot, so the rows and the mark can be read
// together (store.Read).
func NodeChangesAfterIn(ctx context.Context, q Querier, accountID, folderID string, after int64, limit int) ([]NodeChange, error) {
	var rows *sql.Rows
	var err error
	staged := false
	if folderID != "" {
		rows, err = q.QueryContext(ctx, nodeChangesSpaceSQL, accountID, folderID, after, limit)
	} else {
		staged = true
		rows, err = q.QueryContext(ctx, nodeChangesAccountSQL, accountID, after, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeChange{}
	for rows.Next() {
		var c NodeChange
		if staged {
			c, err = scanNodeChangeStaged(rows)
		} else {
			c, err = scanNodeChange(rows)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// nodeChangesCounterSQL reads the journal's autoincrement counter: the highest seq ever allocated, which a delete
// never lowers, so the head survives a prune of every row and a restore answers for the seqs the backup names.
const nodeChangesCounterSQL = "SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'node_changes'), 0)"

// JournalCounterIn reads the journal's autoincrement counter on q, the querier of one snapshot. Before the first
// autoincrement insert the sqlite_sequence table does not exist yet, and the counter is zero; no other error of this
// fixed query is swallowed. SQLite has one writer, so every row with seq at or below the counter a snapshot sees is
// committed and visible in it.
func JournalCounterIn(ctx context.Context, q Querier) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx, nodeChangesCounterSQL).Scan(&seq)
	if err != nil && sqliteCode(err) == sqlite3.SQLITE_ERROR && strings.Contains(err.Error(), "sqlite_sequence") {
		return 0, nil
	}
	return seq, err
}

// NodeChangesHead is the journal's head: the highest seq it ever named, never lower than the mark or the highest row
// present. One snapshot reads the counter, the rows and the mark together. Zero before the first row.
func (s *Store) NodeChangesHead(ctx context.Context) (int64, error) {
	var head int64
	err := s.Read(ctx, func(q Querier) error {
		counter, err := JournalCounterIn(ctx, q)
		if err != nil {
			return err
		}
		var maxSeq int64
		if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM node_changes").Scan(&maxSeq); err != nil {
			return err
		}
		mark, err := NodeChangesMarkIn(ctx, q)
		if err != nil {
			return err
		}
		head = max(counter, maxSeq, mark)
		return nil
	})
	return head, err
}

// NodeChangesMark is the highest seq the age prune has removed. A cursor below it can no longer be served.
func (s *Store) NodeChangesMark(ctx context.Context) (int64, error) {
	return NodeChangesMarkIn(ctx, s.r)
}

// NodeChangesMarkIn is NodeChangesMark on the querier of one snapshot.
func NodeChangesMarkIn(ctx context.Context, q Querier) (int64, error) {
	var mark int64
	err := q.QueryRowContext(ctx, "SELECT pruned_through FROM node_changes_mark WHERE id = 1").Scan(&mark)
	return mark, err
}

// The age prune walks a seq-ordered PREFIX: rows arrive in time order, so the rows older than the cutoff form the
// prefix from the lowest seq up to the first young row. The read takes one batch past the mark and stops at the
// first young row; the delete takes the seq range up to the last old row. Both search the primary key, so no write
// scans the journal.
const nodeChangesAgeRead = "SELECT seq, at FROM node_changes WHERE seq > ? ORDER BY seq LIMIT ?"
const nodeChangesAgeDelete = "DELETE FROM node_changes WHERE seq > ? AND seq <= ?"

// PruneNodeChanges removes up to batch rows whose account is gone, then one batch of the rows older than before,
// each in its own short write. The gone-account prune never touches the mark: nobody can read that feed, so no
// cursor needs resetting. The age prune walks the seq-ordered prefix, stops at the first young row, and raises the
// mark to the last seq it removed in the same write, so a cursor is never served past a mark that has not yet taken
// its rows. The mark never lowers. It returns the total number of rows removed.
func (s *Store) PruneNodeChanges(ctx context.Context, before time.Time, batch int) (int64, error) {
	if batch < 1 {
		return 0, nil
	}
	gone, err := s.pruneNodeChangesGone(ctx, batch)
	if err != nil {
		return 0, err
	}
	aged, err := s.pruneNodeChangesAge(ctx, before, batch)
	if err != nil {
		return gone, err
	}
	return gone + aged, nil
}

// nodeChangesGoneAccountsSQL skip-scans the account index from one account id to the next (each MIN seeks the index,
// never the table) and keeps the ids no account holds any more. The read runs outside the writer: the sweep calls it
// every pass, and a full scan of the journal inside a write would hold the single writer.
const nodeChangesGoneAccountsSQL = `WITH RECURSIVE a(id) AS (
		SELECT (SELECT MIN(account_id) FROM node_changes)
		UNION ALL
		SELECT (SELECT MIN(account_id) FROM node_changes WHERE account_id > a.id) FROM a WHERE a.id IS NOT NULL
	) SELECT id FROM a WHERE id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM accounts WHERE accounts.id = a.id)`

// pruneNodeChangesGone removes up to batch rows whose account no longer exists, without touching the mark. The gone
// accounts are found by a read outside the writer, skip-scanning the account index; each gone account's rows then go
// through that index in their own short write, bounded by the batch budget left.
func (s *Store) pruneNodeChangesGone(ctx context.Context, batch int) (int64, error) {
	var gone []string
	if err := s.Read(ctx, func(q Querier) error {
		rows, err := q.QueryContext(ctx, nodeChangesGoneAccountsSQL)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			gone = append(gone, id)
		}
		return rows.Err()
	}); err != nil {
		return 0, err
	}
	var n int64
	for _, id := range gone {
		if n >= int64(batch) {
			break
		}
		var removed int64
		if err := s.write(ctx, func(tx *sql.Tx) error {
			// The account may have come back since the read; its rows are then no longer gone.
			var one int
			err := tx.QueryRowContext(ctx, "SELECT 1 FROM accounts WHERE id = ?", id).Scan(&one)
			if err == nil {
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			res, err := tx.ExecContext(ctx,
				"DELETE FROM node_changes WHERE seq IN (SELECT seq FROM node_changes WHERE account_id = ? ORDER BY seq LIMIT ?)",
				id, int64(batch)-n)
			if err != nil {
				return err
			}
			removed, err = res.RowsAffected()
			return err
		}); err != nil {
			return n, err
		}
		n += removed
	}
	return n, nil
}

// pruneNodeChangesAge removes one batch of the old prefix and raises the mark to the last seq removed, in one write.
func (s *Store) pruneNodeChangesAge(ctx context.Context, before time.Time, batch int) (int64, error) {
	var n int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		mark, err := NodeChangesMarkIn(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, nodeChangesAgeRead, mark, batch)
		if err != nil {
			return err
		}
		var upto int64
		cutoff := millis(before)
		for rows.Next() {
			var seq, at int64
			if err := rows.Scan(&seq, &at); err != nil {
				rows.Close()
				return err
			}
			if at >= cutoff {
				break // the first young row: the old prefix ends here
			}
			upto = seq
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if upto <= mark {
			return nil
		}
		res, err := tx.ExecContext(ctx, nodeChangesAgeDelete, mark, upto)
		if err != nil {
			return err
		}
		if n, err = res.RowsAffected(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE node_changes_mark SET pruned_through = MAX(pruned_through, ?) WHERE id = 1", upto)
		return err
	})
	return n, err
}

// WatchWrites returns a channel signalled after a commit that wrote journal rows, and a function that unsubscribes.
// A long poll waits on it to learn that the journal may have grown. The channel holds one signal: a write that lands
// while the watcher is reading is not lost, and several writes coalesce into one wake.
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

// notifyWrites signals every watcher after a commit that grew the journal. It never blocks: a full channel already
// holds a wake.
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
