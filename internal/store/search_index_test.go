// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// searchFixture opens a store with one account (acc_a), one folder (fld_a) and its root (nod_root), and returns its path so
// a test can reopen it.
func searchFixture(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "search.db")
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return s, path
}

// addFolder makes a published folder with its root, as a test's second folder.
func addFolder(t *testing.T, s *Store, folderID, rootID string, staged int) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO folders (id, account_id, name, name_key, staged, created_at, updated_at)
		VALUES (?, 'acc_a', ?, ?, ?, 0, 0)`, folderID, folderID, folderID, staged); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES (?, 'acc_a', ?, NULL, '', '', 'dir', 0, 0)`, rootID, folderID); err != nil {
		t.Fatal(err)
	}
}

// node makes a node value for a write.
func node(id, folder, parent, name, kind string) *Node {
	now := time.UnixMilli(1_000)
	return &Node{ID: id, AccountID: "acc_a", FolderID: folder, ParentID: parent, Name: name,
		NameKey: name, Kind: kind, Size: 10, CreatedAt: now, ModifiedAt: now, CreatedBy: "usr_a", ModifiedBy: "usr_a",
		OwnerID: "usr_a"}
}

// searchCounts returns the rows of the name table and of its two full-text tables.
func searchCounts(t *testing.T, s *Store) (names, words, trigrams int) {
	t.Helper()
	for _, q := range []struct {
		sql string
		dst *int
	}{
		{"SELECT COUNT(*) FROM search_names", &names},
		{"SELECT COUNT(*) FROM search_words", &words},
		{"SELECT COUNT(*) FROM search_trigram", &trigrams},
	} {
		if err := s.r.QueryRowContext(context.Background(), q.sql).Scan(q.dst); err != nil {
			t.Fatal(err)
		}
	}
	return names, words, trigrams
}

// searchText returns the folded text the word table holds for a node's row, or "" when there is none.
func searchText(t *testing.T, s *Store, nodeID string) string {
	t.Helper()
	var text string
	err := s.r.QueryRowContext(context.Background(), `SELECT w.text FROM search_words w
		JOIN search_names sn ON sn.id = w.rowid WHERE sn.node_id = ?`, nodeID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func write(t *testing.T, s *Store, fn func(tx *Tx) error) {
	t.Helper()
	if err := s.Write(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}

// TestSearchRowsFollowTheNodeThroughItsLifecycle covers the rows a node changes through: insert, rename, move, trash,
// restore, a label change, and purge. Each path leaves the rows and the full-text tables in step.
func TestSearchRowsFollowTheNodeThroughItsLifecycle(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	addFolder(t, s, "fld_b", "nod_rootb", 0)
	write(t, s, func(tx *Tx) error {
		if err := tx.InsertNode(ctx, node("nod_d", "fld_a", "nod_root", "Docs", "dir")); err != nil {
			return err
		}
		return tx.InsertNode(ctx, node("nod_f", "fld_a", "nod_d", "Report 2026.PDF", "file"))
	})
	if n, w, g := searchCounts(t, s); n != 2 || w != 2 || g != 2 {
		t.Fatalf("after insert: names %d words %d trigram %d, want 2 2 2", n, w, g)
	}
	var ext string
	if err := s.r.QueryRowContext(ctx, "SELECT ext FROM search_names WHERE node_id = 'nod_f'").Scan(&ext); err != nil || ext != "pdf" {
		t.Fatalf("ext = %q, %v; want the folded extension pdf", ext, err)
	}
	if got := searchText(t, s, "nod_f"); got != "report 2026.pdf" {
		t.Fatalf("indexed text = %q, want the folded name", got)
	}

	// Rename: the row and both full-text rows follow the new name.
	write(t, s, func(tx *Tx) error {
		n := node("nod_f", "fld_a", "nod_d", "Plan.pdf", "file")
		return tx.UpdateNode(ctx, n)
	})
	if got := searchText(t, s, "nod_f"); got != "plan.pdf" {
		t.Fatalf("after rename, text = %q", got)
	}
	if n, w, g := searchCounts(t, s); n != 2 || w != 2 || g != 2 {
		t.Fatalf("after rename: names %d words %d trigram %d", n, w, g)
	}

	// Move: the index has no folder column, so a move changes no row. The node keeps its row in the new folder.
	write(t, s, func(tx *Tx) error {
		return tx.MoveSubtree(ctx, "acc_a", "nod_d", "fld_a", "fld_b")
	})
	if n, w, g := searchCounts(t, s); n != 2 || w != 2 || g != 2 {
		t.Fatalf("after move: names %d words %d trigram %d", n, w, g)
	}

	// Trash keeps the row (the reach check hides the node); restore keeps it too.
	write(t, s, func(tx *Tx) error { return tx.TrashNode(ctx, "acc_a", "nod_d", "usr_a", time.Now()) })
	if n, _, _ := searchCounts(t, s); n != 2 {
		t.Fatalf("after trash: %d rows, want 2", n)
	}
	write(t, s, func(tx *Tx) error { return tx.Untrash(ctx, "acc_a", "nod_d") })
	if n, _, _ := searchCounts(t, s); n != 2 {
		t.Fatalf("after restore: %d rows, want 2", n)
	}

	// A label change touches no search row: labels are not in the index.
	write(t, s, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, `INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
			VALUES ('lbf_note', 'acc_a', 'Note', 'note', 'text', 0, 0)`)
		if err != nil {
			return err
		}
		return tx.SetLabelValue(ctx, "acc_a", "nod_f", &LabelValue{FieldID: "lbf_note", Text: strPtr("urgent")})
	})
	if n, w, g := searchCounts(t, s); n != 2 || w != 2 || g != 2 {
		t.Fatalf("after label change: names %d words %d trigram %d", n, w, g)
	}

	// Purge of the folder's subtree removes the rows and both full-text rows of every node in it.
	write(t, s, func(tx *Tx) error {
		_, _, err := tx.PurgeNode(ctx, "acc_a", "nod_d")
		return err
	})
	if n, w, g := searchCounts(t, s); n != 0 || w != 0 || g != 0 {
		t.Fatalf("after purge: names %d words %d trigram %d, want all 0", n, w, g)
	}
}

func strPtr(s string) *string { return &s }

// TestStagedNodesHaveNoRows: a node in a staging folder is never indexed, by its insert or by an update.
func TestStagedNodesHaveNoRows(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	addFolder(t, s, "fld_s", "nod_roots", 1)
	write(t, s, func(tx *Tx) error {
		return tx.InsertNode(ctx, node("nod_staged", "fld_s", "nod_roots", "Hidden.txt", "file"))
	})
	write(t, s, func(tx *Tx) error {
		return tx.UpdateNode(ctx, node("nod_staged", "fld_s", "nod_roots", "Renamed.txt", "file"))
	})
	if n, w, g := searchCounts(t, s); n != 0 || w != 0 || g != 0 {
		t.Fatalf("staged node indexed: names %d words %d trigram %d", n, w, g)
	}
}

// TestDeletingAFolderOrAnAccountRemovesItsFullTextRows: a cascade reaches the search rows, not the full-text tables, so
// the store deletes the full-text rows first.
func TestDeletingAFolderOrAnAccountRemovesItsFullTextRows(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	addFolder(t, s, "fld_b", "nod_rootb", 0)
	write(t, s, func(tx *Tx) error {
		return tx.InsertNode(ctx, node("nod_x", "fld_b", "nod_rootb", "Gone.txt", "file"))
	})
	write(t, s, func(tx *Tx) error { return tx.DeleteFolder(ctx, "acc_a", "fld_b") })
	if n, w, g := searchCounts(t, s); n != 0 || w != 0 || g != 0 {
		t.Fatalf("after folder delete: names %d words %d trigram %d, want all 0", n, w, g)
	}
	write(t, s, func(tx *Tx) error {
		return tx.InsertNode(ctx, node("nod_y", "fld_a", "nod_root", "Also.txt", "file"))
	})
	write(t, s, func(tx *Tx) error { return tx.DeleteAccount(ctx, "acc_a") })
	if n, w, g := searchCounts(t, s); n != 0 || w != 0 || g != 0 {
		t.Fatalf("after account delete: names %d words %d trigram %d, want all 0", n, w, g)
	}
}

// TestBulkInsertsAreIndexedInBatchesAndResume: rows made by a bulk path are not written in its write. The backlog holds the
// folder, the job indexes it batch by batch, and a repeat of the queue (or of a batch) adds nothing twice.
func TestBulkInsertsAreIndexedInBatchesAndResume(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	addFolder(t, s, "fld_b", "nod_rootb", 0)
	write(t, s, func(tx *Tx) error {
		for i := range 25 {
			n := node("nod_b"+string(rune('a'+i)), "fld_b", "nod_rootb", "bulk-"+string(rune('a'+i))+".txt", "file")
			if err := tx.InsertNodeNoIndex(ctx, n); err != nil {
				return err
			}
		}
		return queueFolder(ctx, tx, "fld_b")
	})
	if n, _, _ := searchCounts(t, s); n != 0 {
		t.Fatalf("bulk write indexed %d rows in its own write, want 0", n)
	}
	if left, err := s.SearchBacklogLeft(ctx); err != nil || left != 1 {
		t.Fatalf("backlog = %d, %v; want 1", left, err)
	}
	steps := 0
	for {
		done, err := s.SearchIndexStep(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		steps++
		if done {
			break
		}
		if steps > 10 {
			t.Fatal("job does not finish")
		}
	}
	if steps != 4 {
		t.Fatalf("steps = %d, want 4 (10, 10, 5, then the empty check)", steps)
	}
	if n, w, g := searchCounts(t, s); n != 25 || w != 25 || g != 25 {
		t.Fatalf("after backfill: names %d words %d trigram %d, want 25", n, w, g)
	}
	// A repeat of the queue reads every node again and writes nothing.
	write(t, s, func(tx *Tx) error { return queueFolder(ctx, tx, "fld_b") })
	for {
		done, err := s.SearchIndexStep(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if n, w, g := searchCounts(t, s); n != 25 || w != 25 || g != 25 {
		t.Fatalf("after repeat: names %d words %d trigram %d, want 25", n, w, g)
	}
	d, err := s.SearchCheck(ctx)
	if err != nil || d.Missing != 0 || d.Extra != 0 || d.FTSMismatch {
		t.Fatalf("check = %+v, %v; want clean", d, err)
	}
}

// queueFolder queues every node of a folder, by its whole rowid range, as the upgrade backfill does.
func queueFolder(ctx context.Context, tx *Tx, folderID string) error {
	max, err := tx.NodeRowIDMax(ctx)
	if err != nil {
		return err
	}
	return tx.QueueSearchRange(ctx, "acc_a", folderID, 0, max)
}

// TestQueuedWorkSurvivesACrash: the backlog row commits with the change that queued it, so a process that stops before any
// batch runs finds the work after it reopens, and the job is queued again at start.
func TestQueuedWorkSurvivesACrash(t *testing.T) {
	s, path := searchFixture(t)
	ctx := context.Background()
	addFolder(t, s, "fld_b", "nod_rootb", 0)
	write(t, s, func(tx *Tx) error {
		if err := tx.InsertNodeNoIndex(ctx, node("nod_c", "fld_b", "nod_rootb", "crash.txt", "file")); err != nil {
			return err
		}
		return queueFolder(ctx, tx, "fld_b")
	})
	// The crash: the store is closed with the batch never run.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if left, err := s2.SearchBacklogLeft(ctx); err != nil || left != 1 {
		t.Fatalf("after reopen: backlog = %d, %v; want 1", left, err)
	}
	var queued int
	if err := s2.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE kind = 'search.index' AND status = 'queued'").Scan(&queued); err != nil || queued < 1 {
		t.Fatalf("after reopen: queued search jobs = %d, %v; want at least 1", queued, err)
	}
	for {
		done, err := s2.SearchIndexStep(ctx, SearchBatch)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if n, _, _ := searchCounts(t, s2); n != 1 {
		t.Fatalf("after the job: %d rows, want 1", n)
	}
}

// TestSearchBatchUsesTheFolderIndex reads the plan of a batch's read. It must start at the cursor on the folder index, so
// that a batch costs its own size and not the size of the table (the first backfill run scanned the table for each batch).
func TestSearchBatchUsesTheFolderIndex(t *testing.T) {
	s, _ := searchFixture(t)
	rows, err := s.r.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+searchBatchRead,
		"fld_a", "acc_a", 0, 1<<62, 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "; ")
	if !strings.Contains(joined, "nodes_folder") || strings.Contains(joined, "SCAN n") || strings.Contains(joined, "USE TEMP B-TREE") {
		t.Fatalf("batch plan = %q; want a search of nodes_folder with no scan and no sort", joined)
	}
}

// TestSearchCheckFindsAndRepairsDrift: a node written with no index and no queue is missing; a row whose node is gone is
// extra. Repair removes the extra row and queues the missing one; a rebuild empties and refills the index.
func TestSearchCheckFindsAndRepairsDrift(t *testing.T) {
	s, _ := searchFixture(t)
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_m', 'acc_a', 'fld_a', 'nod_root', 'lost.txt', 'lost.txt', 'file', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	d, err := s.SearchCheck(ctx)
	if err != nil || d.Missing != 1 {
		t.Fatalf("check = %+v, %v; want Missing 1", d, err)
	}
	// A row for a staged node is extra: the write path never makes one, so the row is planted directly.
	addFolder(t, s, "fld_s", "nod_roots", 1)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
		VALUES ('nod_st', 'acc_a', 'fld_s', 'nod_roots', 'st.txt', 'st.txt', 'file', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO search_names (node_id, account_id, kind, ext) VALUES ('nod_st', 'acc_a', 'file', '')`); err != nil {
		t.Fatal(err)
	}
	d, err = s.SearchCheck(ctx)
	if err != nil || d.Extra != 1 {
		t.Fatalf("check = %+v, %v; want Extra 1", d, err)
	}
	if err := s.SearchRepair(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		done, err := s.SearchIndexStep(ctx, SearchBatch)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if d, err := s.SearchCheck(ctx); err != nil || d != (SearchDrift{}) {
		t.Fatalf("after repair: check = %+v, %v; want clean", d, err)
	}
	if err := s.SearchRebuild(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		done, err := s.SearchIndexStep(ctx, SearchBatch)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if n, w, g := searchCounts(t, s); n != 1 || w != 1 || g != 1 {
		t.Fatalf("after rebuild: names %d words %d trigram %d, want 1", n, w, g)
	}
}
