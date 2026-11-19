// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// newChangesFixture opens a store with one account, two team folders and a small tree in the first:
// root nod_c1r holds nod_dir (a directory) and nod_top (a file); nod_dir holds nod_file.
func newChangesFixture(t *testing.T) *Store {
	t.Helper()
	s := openTest(t)
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_c', 'C', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_c1', 'acc_c', 'One', 'one', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_c2', 'acc_c', 'Two', 'two', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_c1r', 'acc_c', 'fld_c1', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_c2r', 'acc_c', 'fld_c2', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_dir', 'acc_c', 'fld_c1', 'nod_c1r', 'dir', 'dir', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, created_at, modified_at)
			VALUES ('nod_file', 'acc_c', 'fld_c1', 'nod_dir', 'f.txt', 'f.txt', 'file', 3, 'e1', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, created_at, modified_at)
			VALUES ('nod_top', 'acc_c', 'fld_c1', 'nod_c1r', 't.txt', 't.txt', 'file', 2, 'e2', 0, 0)`,
	} {
		if err := exec(s, q); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// changesAfter reads the journal of one folder (or of the whole account when folder is "") after seq.
func changesAfter(t *testing.T, s *Store, folder string, seq int64) []NodeChange {
	t.Helper()
	rows, err := s.NodeChangesAfter(context.Background(), "acc_c", folder, seq, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// changeNodeIDs lists the node IDs of the rows, in seq order.
func changeNodeIDs(rows []NodeChange) []string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.NodeID
	}
	return ids
}

// TestNodeChangesInsertRenameContent: an insert, a rename and a content write each leave a row for the node, timed by
// the clock the trigger reads, not by the node's own times.
func TestNodeChangesInsertRenameContent(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	base := int64(0)
	if rows := changesAfter(t, s, "fld_c1", base); len(rows) != 4 {
		t.Fatalf("the fixture's own inserts left %d rows in fld_c1, want 4 (its root and three nodes)", len(rows))
	}
	last := changesAfter(t, s, "fld_c1", 0)
	base = last[len(last)-1].Seq

	n := &Node{ID: "nod_new", AccountID: "acc_c", FolderID: "fld_c1", ParentID: "nod_c1r", Name: "n.txt",
		NameKey: "n.txt", Kind: KindFile, Size: 1, ETag: "e", CreatedAt: Now(), ModifiedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, n) }); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "fld_c1", base)
	if len(rows) != 1 || rows[0].NodeID != "nod_new" || rows[0].FolderID != "fld_c1" || rows[0].Relist {
		t.Fatalf("insert rows = %+v, want one for nod_new", rows)
	}
	if rows[0].At.Before(time.Now().Add(-time.Minute)) || rows[0].At.After(time.Now().Add(time.Minute)) {
		t.Errorf("row time = %v, want the clock's now, not the node's times", rows[0].At)
	}
	base = rows[0].Seq

	n.Name, n.NameKey = "renamed.txt", "renamed.txt"
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateNode(ctx, n) }); err != nil {
		t.Fatal(err)
	}
	rows = changesAfter(t, s, "fld_c1", base)
	if len(rows) != 1 || rows[0].NodeID != "nod_new" {
		t.Fatalf("rename rows = %+v, want one for nod_new", rows)
	}
	base = rows[0].Seq

	n.Size, n.ETag, n.ContentTag = 9, "e9", NewContentTag()
	if err := s.Write(ctx, func(tx *Tx) error { return tx.UpdateNode(ctx, n) }); err != nil {
		t.Fatal(err)
	}
	rows = changesAfter(t, s, "fld_c1", base)
	if len(rows) != 1 || rows[0].NodeID != "nod_new" {
		t.Fatalf("content write rows = %+v, want one for nod_new", rows)
	}
}

// TestNodeChangesMoveWithinSpace: a move inside one folder leaves one row, for that folder.
func TestNodeChangesMoveWithinSpace(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	base := changesAfter(t, s, "fld_c1", 0)
	seq := base[len(base)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		n, err := tx.Node(ctx, "acc_c", "nod_file")
		if err != nil {
			return err
		}
		n.ParentID = "nod_c1r"
		return tx.UpdateNode(ctx, n)
	}); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "fld_c1", seq)
	if len(rows) != 1 || rows[0].NodeID != "nod_file" || rows[0].FolderID != "fld_c1" {
		t.Fatalf("move rows = %+v, want one for nod_file in fld_c1", rows)
	}
	if rows := changesAfter(t, s, "fld_c2", seq); len(rows) != 0 {
		t.Errorf("the other folder saw %d rows, want none", len(rows))
	}
}

// TestNodeChangesMoveToAnotherSpace: a move to another folder leaves a row for BOTH folders, the destination's first.
func TestNodeChangesMoveToAnotherSpace(t *testing.T) {
	s := newChangesFixture(t)
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := exec(s, `UPDATE nodes SET folder_id = 'fld_c2', parent_id = 'nod_c2r' WHERE id = 'nod_file'`); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "", seq)
	if len(rows) != 2 {
		t.Fatalf("move rows = %+v, want two", rows)
	}
	if rows[0].FolderID != "fld_c2" || rows[0].NodeID != "nod_file" {
		t.Errorf("first row = %+v, want the destination fld_c2", rows[0])
	}
	if rows[1].FolderID != "fld_c1" || rows[1].NodeID != "nod_file" {
		t.Errorf("second row = %+v, want the source fld_c1", rows[1])
	}
	if rows[0].Relist || rows[1].Relist {
		t.Errorf("a move marked relist: %+v", rows)
	}
}

// TestNodeChangesTrashRootOnly: trashing a directory leaves a row for the directory alone; the rows of the nodes
// below it did not change.
func TestNodeChangesTrashRootOnly(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.TrashNode(ctx, "acc_c", "nod_dir", "usr", Now())
	}); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "", seq)
	if len(rows) != 1 || rows[0].NodeID != "nod_dir" {
		t.Fatalf("trash rows = %+v, want one for nod_dir only", rows)
	}
	if rows[0].Relist {
		t.Error("trashing marked relist")
	}
}

// TestNodeChangesUntrashFolderRelist: a directory back from the trash leaves a row with relist set, since the rows of
// its children did not change; a file back from the trash leaves a plain row.
func TestNodeChangesUntrashFolderRelist(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.TrashNode(ctx, "acc_c", "nod_dir", "usr", Now())
	}); err != nil {
		t.Fatal(err)
	}
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		n, err := tx.Node(ctx, "acc_c", "nod_dir")
		if err != nil {
			return err
		}
		n.NameKey = n.TrashedNameKey
		if err := tx.UpdateNode(ctx, n); err != nil {
			return err
		}
		return tx.Untrash(ctx, "acc_c", "nod_dir")
	}); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "", seq)
	var dirRows []NodeChange
	for _, r := range rows {
		if r.NodeID == "nod_dir" {
			dirRows = append(dirRows, r)
		}
	}
	if len(dirRows) == 0 {
		t.Fatalf("untrash left no row for nod_dir: %+v", rows)
	}
	if !dirRows[len(dirRows)-1].Relist {
		t.Errorf("the last nod_dir row is not relist: %+v", dirRows)
	}
	for _, r := range rows {
		if r.NodeID == "nod_file" {
			t.Errorf("the child's row changed on the untrash: %+v", r)
		}
	}

	// A file back from the trash is a plain row.
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.TrashNode(ctx, "acc_c", "nod_top", "usr", Now())
	}); err != nil {
		t.Fatal(err)
	}
	all = changesAfter(t, s, "", 0)
	seq = all[len(all)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		n, err := tx.Node(ctx, "acc_c", "nod_top")
		if err != nil {
			return err
		}
		n.NameKey = n.TrashedNameKey
		if err := tx.UpdateNode(ctx, n); err != nil {
			return err
		}
		return tx.Untrash(ctx, "acc_c", "nod_top")
	}); err != nil {
		t.Fatal(err)
	}
	rows = changesAfter(t, s, "", seq)
	for _, r := range rows {
		if r.NodeID == "nod_top" && r.Relist {
			t.Errorf("a file's untrash marked relist: %+v", r)
		}
	}
}

// TestNodeChangesPurgeAndCascade: a purge leaves a row for every deleted node, the cascade included, and so does a
// plain delete.
func TestNodeChangesPurgeAndCascade(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		_, _, err := tx.PurgeNode(ctx, "acc_c", "nod_dir")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "", seq)
	ids := changeNodeIDs(rows)
	if !slices.Contains(ids, "nod_dir") || !slices.Contains(ids, "nod_file") {
		t.Fatalf("purge rows = %v, want nod_dir and nod_file", ids)
	}
	for _, r := range rows {
		if r.NodeID != "nod_dir" && r.NodeID != "nod_file" {
			t.Errorf("purge left a row for %s", r.NodeID)
		}
	}

	seq = rows[len(rows)-1].Seq
	if err := exec(s, `DELETE FROM nodes WHERE id = 'nod_top'`); err != nil {
		t.Fatal(err)
	}
	rows = changesAfter(t, s, "", seq)
	if len(rows) != 1 || rows[0].NodeID != "nod_top" || rows[0].FolderID != "fld_c1" {
		t.Fatalf("delete rows = %+v, want one for nod_top", rows)
	}
}

// TestNodeChangesRolledBackWriteLeavesNoRow: a write that rolls back leaves no journal row.
func TestNodeChangesRolledBackWriteLeavesNoRow(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	boom := errors.New("boom")
	err := s.Write(ctx, func(tx *Tx) error {
		n := &Node{ID: "nod_rb", AccountID: "acc_c", FolderID: "fld_c1", ParentID: "nod_c1r", Name: "rb",
			NameKey: "rb", Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now()}
		if err := tx.InsertNode(ctx, n); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("write err = %v, want boom", err)
	}
	if rows := changesAfter(t, s, "", seq); len(rows) != 0 {
		t.Fatalf("rolled-back write left rows: %+v", rows)
	}
}

// TestNodeChangesUnlistedColumnLeavesNoRow: an update of a column the trigger does not list leaves no row.
func TestNodeChangesUnlistedColumnLeavesNoRow(t *testing.T) {
	s := newChangesFixture(t)
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := exec(s, `UPDATE nodes SET modified_by = 'x', created_by = 'y', trashed_by = 'z',
		type_group = 'image' WHERE id = 'nod_file'`); err != nil {
		t.Fatal(err)
	}
	if rows := changesAfter(t, s, "", seq); len(rows) != 0 {
		t.Fatalf("unlisted columns left rows: %+v", rows)
	}
	// name_key alone is not listed either.
	if err := exec(s, `UPDATE nodes SET name_key = 'other' WHERE id = 'nod_top'`); err != nil {
		t.Fatal(err)
	}
	if rows := changesAfter(t, s, "", seq); len(rows) != 0 {
		t.Fatalf("name_key alone left rows: %+v", rows)
	}
}

// TestNodeChangesNoopUpdateLeavesNoRow: UpdateNode sets every column, but a save that changes only unlisted fields
// (modified_by, type_group) writes no journal row, and neither does a save that changes nothing at all.
func TestNodeChangesNoopUpdateLeavesNoRow(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		n, err := tx.Node(ctx, "acc_c", "nod_file")
		if err != nil {
			return err
		}
		n.ModifiedBy = "usr_other"
		return tx.UpdateNode(ctx, n)
	}); err != nil {
		t.Fatal(err)
	}
	if rows := changesAfter(t, s, "", seq); len(rows) != 0 {
		t.Fatalf("a modified_by-only save left rows: %+v", rows)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		n, err := tx.Node(ctx, "acc_c", "nod_file")
		if err != nil {
			return err
		}
		return tx.UpdateNode(ctx, n)
	}); err != nil {
		t.Fatal(err)
	}
	if rows := changesAfter(t, s, "", seq); len(rows) != 0 {
		t.Fatalf("a save that changes nothing left rows: %+v", rows)
	}
}

// TestNodeChangesEditorsCanShare: editors_can_share is a listed column: a write of it leaves a row, and
// ClearSubtreeEditorsCanShare reports every node whose flag it turns off (and only those).
func TestNodeChangesEditorsCanShare(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := exec(s, `UPDATE nodes SET editors_can_share = 1 WHERE id IN ('nod_dir', 'nod_file')`); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "", seq)
	if len(rows) != 2 {
		t.Fatalf("editors_can_share writes left %d rows, want 2: %+v", len(rows), rows)
	}
	seq = rows[len(rows)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.ClearSubtreeEditorsCanShare(ctx, "acc_c", "nod_dir")
	}); err != nil {
		t.Fatal(err)
	}
	rows = changesAfter(t, s, "", seq)
	ids := changeNodeIDs(rows)
	if !slices.Contains(ids, "nod_dir") || !slices.Contains(ids, "nod_file") {
		t.Fatalf("clearing the subtree left rows %v, want nod_dir and nod_file", ids)
	}
	for _, r := range rows {
		if r.NodeID != "nod_dir" && r.NodeID != "nod_file" {
			t.Errorf("clearing the flag left a row for %s, whose flag was already off", r.NodeID)
		}
	}
	// Clearing again, with every flag already off, writes nothing.
	seq = rows[len(rows)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.ClearSubtreeEditorsCanShare(ctx, "acc_c", "nod_dir")
	}); err != nil {
		t.Fatal(err)
	}
	if rows := changesAfter(t, s, "", seq); len(rows) != 0 {
		t.Fatalf("clearing an already-off subtree left rows: %+v", rows)
	}
}

// TestNodeChangesCopyPublish: a copy job's publish leaves rows the destination's space feed reports, one per published
// node; the staging inserts themselves never reached that feed.
func TestNodeChangesCopyPublish(t *testing.T) {
	fx := newCopyFixture(t)
	ctx := context.Background()
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		_, _, err = tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Before the publish, the destination's feed holds none of the copies: the staging inserts went to the staging
	// folder, and the copies carry derived ids, not the source ids already in the destination.
	published := []string{fx.job.RootID}
	for _, src := range []string{"nod_s1", "nod_s2", "nod_sub", "nod_s3"} {
		published = append(published, fx.job.DerivedNodeID(src))
	}
	rows, err := fx.s.NodeChangesAfter(ctx, "acc_a", "fld_a", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	beforeIDs := changeNodeIDs(rows)
	for _, id := range published {
		if slices.Contains(beforeIDs, id) {
			t.Errorf("staged node %s reached the destination feed before the publish", id)
		}
	}
	if err := fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		return tx.PublishStaged(ctx, cur, "fld_a", "nod_root", "copy", "copy", Now())
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = fx.s.NodeChangesAfter(ctx, "acc_a", "fld_a", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	ids := changeNodeIDs(rows)
	for _, want := range published {
		if !slices.Contains(ids, want) {
			t.Errorf("the publish left no row for %s; rows %v", want, ids)
		}
	}
}

// TestNodeChangesImportPublish: an import's publish (the staged tree moves, the top-level nodes are placed, the staging
// goes) leaves rows the destination's space feed reports.
func TestNodeChangesImportPublish(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.CreateStagingFolder(ctx, "acc_c", "fld_stage", "nod_stage_root", Now()); err != nil {
			return err
		}
		n := &Node{ID: "nod_imp", AccountID: "acc_c", FolderID: "fld_stage", ParentID: "nod_stage_root",
			Name: "imp.txt", NameKey: "imp.txt", Kind: KindFile, Size: 4, ETag: "ei", CreatedAt: Now(), ModifiedAt: Now()}
		return tx.InsertNodeNoIndex(ctx, n)
	}); err != nil {
		t.Fatal(err)
	}
	// Staged rows never reach the account feed.
	for _, r := range changesAfter(t, s, "", 0) {
		if r.NodeID == "nod_imp" {
			t.Fatal("the staged insert reached the account feed")
		}
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.SetStagedParent(ctx, "acc_c", "fld_stage", "nod_imp", "nod_c1r", "imp.txt", "imp.txt", Now()); err != nil {
			return err
		}
		if err := tx.MoveStagedTree(ctx, "acc_c", "fld_stage", "fld_c1", Now()); err != nil {
			return err
		}
		return tx.DropStaging(ctx, "acc_c", "fld_stage", "nod_stage_root")
	}); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "fld_c1", 0)
	if !slices.Contains(changeNodeIDs(rows), "nod_imp") {
		t.Fatalf("the import publish left no row for nod_imp; rows %v", changeNodeIDs(rows))
	}
	found := false
	for _, r := range changesAfter(t, s, "", 0) {
		if r.NodeID == "nod_imp" {
			found = true
		}
	}
	if !found {
		t.Fatal("the published node never reached the account feed")
	}
}

// TestNodeChangesMoveFolderContents: moving a folder's contents into another folder leaves rows for BOTH folders, and
// the destination's feed reports the moved nodes.
func TestNodeChangesMoveFolderContents(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	all := changesAfter(t, s, "", 0)
	seq := all[len(all)-1].Seq
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.MoveFolderContents(ctx, "acc_c", "fld_c1", "fld_c2", "nod_c2r", "usr")
	}); err != nil {
		t.Fatal(err)
	}
	rows := changesAfter(t, s, "", seq)
	var dest, src int
	for _, r := range rows {
		switch r.FolderID {
		case "fld_c2":
			dest++
		case "fld_c1":
			src++
		}
	}
	if dest == 0 || src == 0 {
		t.Fatalf("move rows: %d for the destination, %d for the source; want both", dest, src)
	}
	destIDs := changeNodeIDs(changesAfter(t, s, "fld_c2", seq))
	for _, want := range []string{"nod_dir", "nod_file", "nod_top"} {
		if !slices.Contains(destIDs, want) {
			t.Errorf("the destination feed misses %s; rows %v", want, destIDs)
		}
	}
}

// TestNodeChangesReads: the folder feed reads in seq order after a seq, up to the limit; the account feed reads raw
// rows and marks the staged ones, so the reader can skip them while the cursor still advances past them; the head
// and the mark read as the journal holds them.
func TestNodeChangesReads(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	rows := changesAfter(t, s, "fld_c1", 0)
	if len(rows) != 4 {
		t.Fatalf("fixture rows = %d, want 4", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Fatalf("rows not in seq order: %+v", rows)
		}
	}
	after := rows[1].Seq
	page, err := s.NodeChangesAfter(ctx, "acc_c", "fld_c1", after, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Seq != rows[2].Seq || page[1].Seq != rows[3].Seq {
		t.Fatalf("page = %+v, want the two rows after %d", page, after)
	}
	head, err := s.NodeChangesHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if head != rows[len(rows)-1].Seq {
		t.Errorf("head = %d, want %d", head, rows[len(rows)-1].Seq)
	}
	mark, err := s.NodeChangesMark(ctx)
	if err != nil || mark != 0 {
		t.Errorf("mark = %d, %v; want 0", mark, err)
	}

	// A staged folder's rows are marked staged in the account feed, and a folder feed of another account reads nothing.
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.CreateStagingFolder(ctx, "acc_c", "fld_st", "nod_st_root", Now())
	}); err != nil {
		t.Fatal(err)
	}
	staged := 0
	for _, r := range changesAfter(t, s, "", 0) {
		if r.FolderID == "fld_st" {
			if !r.Staged {
				t.Fatalf("a staged row is not marked staged: %+v", r)
			}
			staged++
		} else if r.Staged {
			t.Fatalf("a live folder's row is marked staged: %+v", r)
		}
	}
	if staged == 0 {
		t.Fatal("the staging folder left no rows to mark")
	}
	other, err := s.NodeChangesAfter(ctx, "acc_nope", "fld_c1", 0, 100)
	if err != nil || len(other) != 0 {
		t.Errorf("another account read %d rows, %v; want none", len(other), err)
	}
}

// TestNodeChangesHeadNeverLowers: the head is the highest seq the journal ever named, so a prune that takes every row
// leaves it where it was, at or above the mark; a start cursor at it still reads.
func TestNodeChangesHeadNeverLowers(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	head, err := s.NodeChangesHead(ctx)
	if err != nil || head == 0 {
		t.Fatalf("head = %d, %v; want the fixture's highest seq", head, err)
	}
	for {
		n, err := s.PruneNodeChanges(ctx, time.Now().Add(time.Hour), 100)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	head2, err := s.NodeChangesHead(ctx)
	if err != nil || head2 != head {
		t.Fatalf("head after pruning everything = %d, %v; want %d", head2, err, head)
	}
	mark, err := s.NodeChangesMark(ctx)
	if err != nil || mark != head {
		t.Fatalf("mark = %d, %v; want the head %d", mark, err, head)
	}
	rows, err := s.NodeChangesAfter(ctx, "acc_c", "", head2, 100)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows after the head = %+v, %v; want none", rows, err)
	}
}

// TestNodeChangesPlansUseTheIndexes: the space feed searches the folder-and-seq index, the account feed the
// account-and-seq index, and the age prune's read and delete search the primary key: no plan scans the journal.
func TestNodeChangesPlansUseTheIndexes(t *testing.T) {
	s := newChangesFixture(t)
	if p := explainPlan(t, s, nodeChangesSpaceSQL, "acc_c", "fld_c1", int64(0), 200); !strings.Contains(p, "node_changes_folder") || strings.Contains(p, "SCAN") {
		t.Errorf("space feed plan = %q, want a search of node_changes_folder and no scan", p)
	}
	if p := explainPlan(t, s, nodeChangesAccountSQL, "acc_c", int64(0), 200); !strings.Contains(p, "node_changes_account") || strings.Contains(p, "SCAN") {
		t.Errorf("account feed plan = %q, want a search of node_changes_account and no scan", p)
	}
	if p := explainPlan(t, s, nodeChangesAgeRead, int64(0), 5000); strings.Contains(p, "SCAN") {
		t.Errorf("age prune read plan = %q, want no scan of node_changes", p)
	}
	if p := explainPlan(t, s, nodeChangesAgeDelete, int64(0), int64(100)); strings.Contains(p, "SCAN") {
		t.Errorf("age prune delete plan = %q, want no scan of node_changes", p)
	}
}

// goneAccountRows makes a second account with one node, then deletes the account: its journal rows (the insert and
// the cascade delete) stay behind, young, until a prune takes them.
func goneAccountRows(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_g', 'G', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_g', 'acc_g', 'G', 'g', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_gr', 'acc_g', 'fld_g', NULL, '', '', 'dir', 0, 0)`,
		`DELETE FROM accounts WHERE id = 'acc_g'`,
	} {
		if err := exec(s, q); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPruneNodeChanges: the age prune walks the seq-ordered prefix and stops at the first young row, raising the mark
// to the last seq it removed in the same write; the deleted account's rows go without touching the mark, so another
// account's cursor keeps reading.
func TestPruneNodeChanges(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	// Age the fixture's first three rows past the cutoff; the rest stay young.
	if err := exec(s, `UPDATE node_changes SET at = 1000 WHERE seq <= 3`); err != nil {
		t.Fatal(err)
	}
	goneAccountRows(t, s)
	var goneSeq int64
	if err := s.r.QueryRowContext(ctx, `SELECT MAX(seq) FROM node_changes WHERE node_id = 'nod_gr'`).Scan(&goneSeq); err != nil {
		t.Fatal(err)
	}

	// One batch of two: the gone account's two rows go, and the age batch takes the two oldest rows. The mark rises
	// to the last aged seq, NOT to the deleted account's higher seqs.
	n, err := s.PruneNodeChanges(ctx, time.UnixMilli(2000), 2)
	if err != nil || n != 4 {
		t.Fatalf("pruned %d, %v; want 4 (two gone, two aged)", n, err)
	}
	mark, err := s.NodeChangesMark(ctx)
	if err != nil || mark != 2 {
		t.Fatalf("mark = %d, %v; want 2, the last aged seq", mark, err)
	}
	if goneSeq <= mark {
		t.Fatalf("the gone account's seqs %d do not sit above the mark %d; the test proves nothing", goneSeq, mark)
	}
	// The next batch takes the last old row and stops at the first young one; the gone rows are already gone.
	n, err = s.PruneNodeChanges(ctx, time.UnixMilli(2000), 2)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want 1 (the last old row)", n, err)
	}
	mark, err = s.NodeChangesMark(ctx)
	if err != nil || mark != 3 {
		t.Fatalf("mark = %d, %v; want 3", mark, err)
	}
	// Nothing old remains: a further prune takes nothing and keeps the mark.
	n, err = s.PruneNodeChanges(ctx, time.UnixMilli(2000), 10)
	if err != nil || n != 0 {
		t.Fatalf("pruned %d, %v; want 0", n, err)
	}
	rows, err := s.NodeChangesAfter(ctx, "acc_c", "", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Seq <= 3 {
			t.Errorf("old row %d survived the prune", r.Seq)
		}
		if r.AccountID == "acc_g" {
			t.Errorf("the deleted account's row %d survived the prune", r.Seq)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("young rows left = %+v, want the two young ones", rows)
	}
	// An old row BEHIND a young one is not taken: the walk stops at the first young row.
	if err := exec(s, `UPDATE node_changes SET at = 500 WHERE seq = 5`); err != nil {
		t.Fatal(err)
	}
	n, err = s.PruneNodeChanges(ctx, time.UnixMilli(2000), 10)
	if err != nil || n != 0 {
		t.Fatalf("pruned %d, %v; want 0 past the first young row", n, err)
	}
	mark2, err := s.NodeChangesMark(ctx)
	if err != nil || mark2 != mark {
		t.Errorf("mark moved past a young row: %d -> %d (%v)", mark, mark2, err)
	}
	if rows := changesAfter(t, s, "", 0); !slices.Contains(changeNodeIDs(rows), "nod_top") {
		t.Errorf("the old row behind the young one was taken: %+v", rows)
	}
	// The young rows are still readable after the mark.
	if _, err := s.NodeChangesAfter(ctx, "acc_c", "", mark, 1000); err != nil {
		t.Fatal(err)
	}
}

// TestPruneNodeChangesGoneKeepsTheMark: with nothing old, a prune still removes a deleted account's young rows and
// leaves the mark where it was, so the live account's cursor keeps reading.
func TestPruneNodeChangesGoneKeepsTheMark(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	goneAccountRows(t, s)
	head, err := s.NodeChangesHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneNodeChanges(ctx, time.Now().Add(-time.Hour), 1000)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d, %v; want the gone account's two rows", n, err)
	}
	mark, err := s.NodeChangesMark(ctx)
	if err != nil || mark != 0 {
		t.Fatalf("mark = %d, %v; want 0, untouched by the gone-account prune", mark, err)
	}
	rows, err := s.NodeChangesAfter(ctx, "acc_c", "", 0, 1000)
	if err != nil || len(rows) != 5 {
		t.Fatalf("the live account's rows = %d, %v; want all 5", len(rows), err)
	}
	if head2, err := s.NodeChangesHead(ctx); err != nil || head2 != head {
		t.Errorf("head moved with the gone prune: %d -> %d (%v)", head, head2, err)
	}
}

// TestNodeChangeTriggersSurviveMigrations: after every migration ran, the three journal triggers sit on nodes. A
// later migration that rebuilds the table drops them, and must recreate them.
func TestNodeChangeTriggersSurviveMigrations(t *testing.T) {
	s := openTest(t)
	rows, err := s.r.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'nodes' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"nodes_changes_delete", "nodes_changes_insert", "nodes_changes_update"}
	if !slices.Equal(got, want) {
		t.Fatalf("triggers on nodes = %v, want %v", got, want)
	}
}

// TestWatchWrites: a watcher is signalled after a commit that wrote journal rows, not after one that wrote none, not
// after one rolls back, and not after it unsubscribes.
func TestWatchWrites(t *testing.T) {
	s := newChangesFixture(t)
	ctx := context.Background()
	ch, stop := s.WatchWrites()
	drain := func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
	if drain() {
		t.Fatal("a fresh watcher already holds a signal")
	}
	boom := errors.New("boom")
	if err := s.Write(ctx, func(tx *Tx) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("write err = %v", err)
	}
	if drain() {
		t.Fatal("a rolled-back write signalled the watcher")
	}
	// A commit that writes no journal row (an account rename) does not wake a feed waiter.
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, `UPDATE accounts SET name = 'renamed' WHERE id = 'acc_c'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if drain() {
		t.Fatal("a commit with no journal row signalled the watcher")
	}
	// A prune removes rows but writes none: it does not wake a waiter either.
	if _, err := s.PruneNodeChanges(ctx, time.Now().Add(time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if drain() {
		t.Fatal("a prune signalled the watcher")
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, `UPDATE nodes SET name = 'x' WHERE id = 'nod_top'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("a committed journal write did not signal the watcher")
	}
	stop()
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, `UPDATE nodes SET name = 'y' WHERE id = 'nod_top'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if drain() {
		t.Fatal("an unsubscribed watcher was signalled")
	}
}
