// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/names"
)

// Search indexing (design section 14, slice q1, decision 40). The name rows and their full-text rows are kept in step
// with the nodes in two ways:
//
//   - A single-node change (InsertNode, UpdateNode) indexes the node in the same write as the change. So does a small
//     copy (up to SearchSmallCopy nodes), which is one write anyway.
//   - A bulk write (a larger copy, a chunked copy's publish) does not index in its write. It records the rowid range of
//     its nodes on search_backlog (QueueSearchRange) in the same write, and the search job reads the range in batches of
//     SearchBatch, each in its own short write (SearchIndexStep). A crash between the two loses nothing: the backlog row
//     is committed with the change.
//
// A node keeps its rowid for its whole life, and a copy's publish moves rows without changing them, so a range read
// on rowid finds exactly the nodes a copy made (plus any other node of the folder inside the range, which is skipped
// when it already has a row).
//
// Rows of a staged node are never written. Purge, folder deletion and account deletion remove the full-text rows
// themselves, because a cascade does not reach them. A subtree move changes no search row: the rows are keyed by node
// ID, the name does not change, and access is checked at query time.

// SearchJobKind is the kind of the job that drains search_backlog.
const SearchJobKind = "search.index"

// SearchBatch is how many nodes one search write indexes.
const SearchBatch = 1000

// SearchSmallCopy is the most nodes a copy may have and still index them in its own write, rather than queue them.
const SearchSmallCopy = 100

// searchBatchRead is the read a batch makes: the nodes of a folder whose rowid lies in (cursor, upto], in rowid order. On
// the nodes_folder index the rowid is the entry after folder_id, so the batch is one index range;
// TestSearchBatchUsesTheFolderIndex reads its plan.
const searchBatchRead = `SELECT n.rowid, n.id, n.name, n.kind FROM nodes n
	WHERE n.folder_id = ? AND n.account_id = ? AND n.rowid > ? AND n.rowid <= ? AND n.parent_id IS NOT NULL
	ORDER BY n.rowid LIMIT ?`

// searchExt is the extension a file's search row keeps: the part after its last dot, folded, when the name has one
// that is at most 16 characters. A directory or a shortcut has none.
func searchExt(name, kind string) string {
	if kind != KindFile {
		return ""
	}
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return ""
	}
	ext := names.SearchForm(name[i+1:])
	if utf8.RuneCountInString(ext) > 16 {
		return ""
	}
	return ext
}

// indexNode brings the search rows of node nodeID in line with the node: a node of a published folder is indexed, and
// anything else (a staged node, a node that is gone) has no row.
func (tx *Tx) indexNode(ctx context.Context, nodeID string) error {
	var accountID, name, kind string
	var staged int
	var parent sql.NullString
	err := tx.q.QueryRowContext(ctx, `SELECT n.account_id, n.name, n.kind, f.staged, n.parent_id FROM nodes n
		JOIN folders f ON f.id = n.folder_id WHERE n.id = ?`, nodeID).Scan(&accountID, &name, &kind, &staged, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.unindexNode(ctx, nodeID)
	}
	if err != nil {
		return err
	}
	// A folder's root has no parent and no name: it is not an item a search can name, so it has no row.
	if staged == 1 || !parent.Valid {
		return tx.unindexNode(ctx, nodeID)
	}
	return tx.putName(ctx, nodeID, accountID, name, kind)
}

// putName writes the search row of a node and its two full-text rows, or rewrites them when the name changed. The
// full-text rows are contentful, so the old text is read back from them to decide whether they need a rewrite.
func (tx *Tx) putName(ctx context.Context, nodeID, accountID, name, kind string) error {
	text := names.SearchForm(name)
	ext := searchExt(name, kind)
	var rowid int64
	err := tx.q.QueryRowContext(ctx, "SELECT id FROM search_names WHERE node_id = ?", nodeID).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.q.ExecContext(ctx, `INSERT INTO search_names (node_id, account_id, kind, ext) VALUES (?, ?, ?, ?)`,
			nodeID, accountID, kind, ext)
		if err != nil {
			return err
		}
		if rowid, err = res.LastInsertId(); err != nil {
			return err
		}
		return tx.insertText(ctx, rowid, accountID, text)
	}
	if err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, "UPDATE search_names SET kind = ?, ext = ? WHERE id = ?", kind, ext, rowid); err != nil {
		return err
	}
	var old string
	err = tx.q.QueryRowContext(ctx, "SELECT text FROM search_words WHERE rowid = ?", rowid).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && old == text {
		return nil
	}
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_words WHERE rowid = ?", rowid); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_trigram WHERE rowid = ?", rowid); err != nil {
		return err
	}
	return tx.insertText(ctx, rowid, accountID, text)
}

// insertText writes the two full-text rows of one search row.
func (tx *Tx) insertText(ctx context.Context, rowid int64, accountID, text string) error {
	if _, err := tx.q.ExecContext(ctx, "INSERT INTO search_words (rowid, text, account_id) VALUES (?, ?, ?)",
		rowid, text, accountID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, "INSERT INTO search_trigram (rowid, text, account_id) VALUES (?, ?, ?)",
		rowid, text, accountID)
	return err
}

// unindexNode removes the search row of node nodeID and its full-text rows. A node with no row is not an error.
func (tx *Tx) unindexNode(ctx context.Context, nodeID string) error {
	var rowid int64
	err := tx.q.QueryRowContext(ctx, "SELECT id FROM search_names WHERE node_id = ?", nodeID).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_words WHERE rowid = ?", rowid); err != nil {
		return err
	}
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_trigram WHERE rowid = ?", rowid); err != nil {
		return err
	}
	_, err = tx.q.ExecContext(ctx, "DELETE FROM search_names WHERE id = ?", rowid)
	return err
}

// dropSearchSubtree removes the full-text rows of node id and of every node below it. It runs before the nodes are
// deleted, while their search rows still name them; the cascade then removes the search rows themselves. The walk is
// the one the foreign key cascade of nodes.parent_id makes.
func (tx *Tx) dropSearchSubtree(ctx context.Context, accountID, id string) error {

	const under = `SELECT sn.id FROM search_names sn WHERE sn.node_id IN (
		WITH RECURSIVE sub(nid) AS (
			SELECT n.id FROM nodes n WHERE n.account_id = ? AND n.id = ?
			UNION ALL
			SELECT c.id FROM nodes c JOIN sub s ON c.parent_id = s.nid WHERE c.account_id = ?
		) SELECT nid FROM sub)`
	if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_words WHERE rowid IN ("+under+")",
		accountID, id, accountID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, "DELETE FROM search_trigram WHERE rowid IN ("+under+")",
		accountID, id, accountID)
	return err
}

// dropSearchFolder removes the full-text rows of every node of a folder, before the folder is deleted.
func (tx *Tx) dropSearchFolder(ctx context.Context, accountID, folderID string) error {
	if _, err := tx.q.ExecContext(ctx, `DELETE FROM search_words WHERE rowid IN (SELECT sn.id FROM search_names sn
		JOIN nodes n ON n.id = sn.node_id WHERE n.account_id = ? AND n.folder_id = ?)`, accountID, folderID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, `DELETE FROM search_trigram WHERE rowid IN (SELECT sn.id FROM search_names sn
		JOIN nodes n ON n.id = sn.node_id WHERE n.account_id = ? AND n.folder_id = ?)`, accountID, folderID)
	return err
}

// dropSearchAccount removes the full-text rows of an account, before the account is deleted.
func (tx *Tx) dropSearchAccount(ctx context.Context, accountID string) error {

	if _, err := tx.q.ExecContext(ctx, `DELETE FROM search_words WHERE rowid IN (
		SELECT id FROM search_names WHERE account_id = ?)`, accountID); err != nil {
		return err
	}
	_, err := tx.q.ExecContext(ctx, `DELETE FROM search_trigram WHERE rowid IN (
		SELECT id FROM search_names WHERE account_id = ?)`, accountID)
	return err
}

// NodeRowIDMax returns the largest rowid of the nodes table (0 when it is empty). A copy takes it before its inserts,
// so the rowids the copy made lie above it.
func (tx *Tx) NodeRowIDMax(ctx context.Context) (int64, error) {
	var max int64
	err := tx.q.QueryRowContext(ctx, "SELECT COALESCE(MAX(rowid), 0) FROM nodes").Scan(&max)
	return max, err
}

// FolderRowIDRange returns the smallest and largest rowid of the nodes of a folder (0 and 0 when it has none). A staged
// tree keeps its rowids when it is published, so this is the range the publish must index.
func (tx *Tx) FolderRowIDRange(ctx context.Context, accountID, folderID string) (lo, hi int64, err error) {
	err = tx.q.QueryRowContext(ctx, "SELECT COALESCE(MIN(rowid), 0), COALESCE(MAX(rowid), 0) FROM nodes WHERE account_id = ? AND folder_id = ?",
		accountID, folderID).Scan(&lo, &hi)
	return lo, hi, err
}

// QueueSearchRange records that the nodes of folderID with rowids in (after, upto] need search rows, in the same write as
// the change that made them. The search job reads the range in batches. A range that is empty queues nothing. Each call
// adds its own item, so another item's cursor is never moved.
func (tx *Tx) QueueSearchRange(ctx context.Context, accountID, folderID string, after, upto int64) error {
	if upto <= after {
		return nil
	}
	if _, err := tx.q.ExecContext(ctx, `INSERT INTO search_backlog (folder_id, account_id, cursor, upto, created_at)
		VALUES (?, ?, ?, ?, ?)`, folderID, accountID, after, upto, millis(tx.clock())); err != nil {
		return err
	}
	return tx.queueSearchJob(ctx)
}

// queueSearchJob queues a search job unless one is already waiting. A running job does not count: it may be past its
// last look at the backlog, so a queued one is needed for work recorded after that look. A job waiting out a retry is
// brought forward, so new work does not wait for the retry.
func (tx *Tx) queueSearchJob(ctx context.Context) error {
	var waiting int
	if err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE kind = ? AND status = 'queued'",
		SearchJobKind).Scan(&waiting); err != nil {
		return err
	}
	if waiting > 0 {
		return tx.WakeJobs(ctx, SearchJobKind)
	}
	now := tx.clock()
	return tx.InsertJob(ctx, &Job{ID: id.New("job"), Kind: SearchJobKind, Status: JobQueued,
		CreatedAt: now, UpdatedAt: now})
}

// ensureSearchJob queues a search job at start when the backlog has work, so an upgrade's backfill runs without an
// operator. It is the start-up form of queueSearchJob.
func (s *Store) ensureSearchJob(ctx context.Context) error {
	return s.Write(ctx, func(tx *Tx) error {
		var left int
		if err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_backlog").Scan(&left); err != nil {
			return err
		}
		if left == 0 {
			return nil
		}
		return tx.queueSearchJob(ctx)
	})
}

// SearchIndexStep indexes the next batch of the first item on the backlog, in one write. It reads up to limit nodes of
// the item's range after its cursor, adds the rows the nodes lack, and moves the cursor to the last node it read. An item
// whose batch comes back short, or whose cursor reaches its upper bound, is finished, and its backlog row goes in the
// same write. done is true when the backlog is empty. Each step is safe to repeat: a node that has a row is skipped.
func (s *Store) SearchIndexStep(ctx context.Context, limit int) (done bool, err error) {
	err = s.Write(ctx, func(tx *Tx) error {
		var itemID, cursor, upto int64
		var folderID, accountID string
		err := tx.q.QueryRowContext(ctx, `SELECT id, folder_id, account_id, cursor, upto FROM search_backlog
			ORDER BY id LIMIT 1`).Scan(&itemID, &folderID, &accountID, &cursor, &upto)
		if errors.Is(err, sql.ErrNoRows) {
			done = true
			return nil
		}
		if err != nil {
			return err
		}
		rows, err := tx.q.QueryContext(ctx, searchBatchRead, folderID, accountID, cursor, upto, limit)
		if err != nil {
			return err
		}
		type item struct {
			rowid    int64
			id, name string
			kind     string
		}
		var batch []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.rowid, &it.id, &it.name, &it.kind); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, it)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, it := range batch {
			var have int
			err := tx.q.QueryRowContext(ctx, "SELECT 1 FROM search_names WHERE node_id = ?", it.id).Scan(&have)
			if err == nil {
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := tx.putName(ctx, it.id, accountID, it.name, it.kind); err != nil {
				return err
			}
		}
		if len(batch) < limit || batch[len(batch)-1].rowid >= upto {
			_, err := tx.q.ExecContext(ctx, "DELETE FROM search_backlog WHERE id = ?", itemID)
			return err
		}
		_, err = tx.q.ExecContext(ctx, "UPDATE search_backlog SET cursor = ? WHERE id = ?", batch[len(batch)-1].rowid, itemID)
		return err
	})
	return done, err
}

// SearchBacklogLeft returns how many folders wait on the search backlog.
func (s *Store) SearchBacklogLeft(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_backlog").Scan(&n)
	return n, err
}

// Read runs fn in one read-only transaction, so every read fn makes sees the same state of the database. Access
// checks that must agree with the rows they filter are read this way.
func (s *Store) Read(ctx context.Context, fn func(q Querier) error) error {
	tx, err := s.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}

// Querier is what a read function runs on: the read pool, or the transaction Read gives it.
type Querier = querier

// SearchDrift is what SearchCheck found wrong with the search rows. A clean index has all three at zero or false.
type SearchDrift struct {
	// Missing counts searchable nodes with no row whose folder is not on the backlog: work nobody has queued.
	Missing int64
	// Extra counts rows whose node is gone or staged.
	Extra int64
	// FTSMismatch is set when a full-text table has a different number of rows from search_names.
	FTSMismatch bool
}

// SearchCheck compares the search rows with the nodes they index (design section 6). Folders on the backlog are not
// counted as missing, because their rows are on their way.
func (s *Store) SearchCheck(ctx context.Context) (SearchDrift, error) {
	var d SearchDrift
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes n JOIN folders f ON f.id = n.folder_id
		WHERE f.staged = 0 AND n.parent_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM search_names sn WHERE sn.node_id = n.id)
		AND NOT EXISTS (SELECT 1 FROM search_backlog b WHERE b.folder_id = n.folder_id)`).Scan(&d.Missing)
	if err != nil {
		return d, err
	}
	err = s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_names sn WHERE NOT EXISTS (
		SELECT 1 FROM nodes n JOIN folders f ON f.id = n.folder_id
		WHERE n.id = sn.node_id AND f.staged = 0 AND n.parent_id IS NOT NULL)`).Scan(&d.Extra)
	if err != nil {
		return d, err
	}
	var names, words, trigrams int64
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_names").Scan(&names); err != nil {
		return d, err
	}
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_words").Scan(&words); err != nil {
		return d, err
	}
	if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_trigram").Scan(&trigrams); err != nil {
		return d, err
	}
	d.FTSMismatch = words != names || trigrams != names
	return d, nil
}

// SearchRebuild empties the search rows and the full-text tables, and queues every folder, so the search job makes them
// again from the nodes. It is the repair for a full-text table that disagrees with search_names (SearchCheck reports it).
func (s *Store) SearchRebuild(ctx context.Context) error {
	return s.Write(ctx, func(tx *Tx) error {
		for _, q := range []string{"DELETE FROM search_words", "DELETE FROM search_trigram", "DELETE FROM search_names"} {
			if _, err := tx.q.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_backlog"); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO search_backlog (folder_id, account_id, cursor, upto, created_at)
			SELECT f.id, f.account_id, 0, MAX(n.rowid), ? FROM folders f JOIN nodes n ON n.folder_id = f.id
			WHERE f.staged = 0 GROUP BY f.id`, millis(tx.clock())); err != nil {
			return err
		}
		return tx.queueSearchJob(ctx)
	})
}

// SearchRepair makes the search rows agree with the nodes. It removes the rows that have no node, and queues every folder
// that has a missing row, so the search job adds them. A full-text table that disagrees with search_names needs
// SearchRebuild.
func (s *Store) SearchRepair(ctx context.Context) error {
	return s.Write(ctx, func(tx *Tx) error {
		const orphans = `SELECT sn.id FROM search_names sn WHERE NOT EXISTS (
			SELECT 1 FROM nodes n JOIN folders f ON f.id = n.folder_id
			WHERE n.id = sn.node_id AND f.staged = 0 AND n.parent_id IS NOT NULL)`
		if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_words WHERE rowid IN ("+orphans+")"); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_trigram WHERE rowid IN ("+orphans+")"); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, "DELETE FROM search_names WHERE id IN ("+orphans+")"); err != nil {
			return err
		}
		// A folder with a missing row gets one item over all its rows; the rows it has are skipped.
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO search_backlog (folder_id, account_id, cursor, upto, created_at)
			SELECT DISTINCT n.folder_id, n.account_id, 0, (SELECT MAX(x.rowid) FROM nodes x WHERE x.folder_id = n.folder_id), ?
			FROM nodes n JOIN folders f ON f.id = n.folder_id
			WHERE f.staged = 0 AND n.parent_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM search_names sn WHERE sn.node_id = n.id)`,
			millis(tx.clock())); err != nil {
			return err
		}
		return tx.queueSearchJob(ctx)
	})
}
