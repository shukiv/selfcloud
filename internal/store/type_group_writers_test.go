// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// typeGroupOf reads a node back through the store's read path and returns its type group.
func typeGroupOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	n, err := s.Node(context.Background(), "acc_a", id)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return n.TypeGroup
}

// TestTypeGroupInsertAndUpdateWriters: every writer of a node's name or media type sets type_group from the new name and
// type in the same write. Insert (the upload and copy paths), the no-index insert, the rename and the media-type change,
// through UpdateNode and UpdateNodeHeld (the office rename and the restore use these).
func TestTypeGroupInsertAndUpdateWriters(t *testing.T) {
	ctx := context.Background()
	s, _ := searchFixture(t)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	file := func(id, name, mime string) *Node {
		n := node(id, "fld_a", "nod_root", name, "file")
		n.MimeType = mime
		return n
	}

	must(s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, file("nod_up", "Report.PDF", "application/octet-stream")) }))
	if got := typeGroupOf(t, s, "nod_up"); got != "pdf" {
		t.Errorf("insert (upload path): type_group %q, want pdf", got)
	}

	must(s.Write(ctx, func(tx *Tx) error { return tx.InsertNodeNoIndex(ctx, file("nod_cp", "song.flac", "")) }))
	if got := typeGroupOf(t, s, "nod_cp"); got != "audio" {
		t.Errorf("insert no-index (copy and import path): type_group %q, want audio", got)
	}

	folder := node("nod_dir", "fld_a", "nod_root", "pics.pdf", "dir")
	must(s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, folder) }))
	if got := typeGroupOf(t, s, "nod_dir"); got != "" {
		t.Errorf("insert of a folder: type_group %q, want empty", got)
	}

	// A rename through UpdateNode (the rename route and a restore's UpdateNode): the name decides the new group.
	must(s.Write(ctx, func(tx *Tx) error {
		n, err := getNode(ctx, tx.q, "acc_a", "nod_up")
		if err != nil {
			return err
		}
		n.Name, n.NameKey = "notes.docx", "notes.docx"
		return tx.UpdateNode(ctx, n)
	}))
	if got := typeGroupOf(t, s, "nod_up"); got != "wordprocessing" {
		t.Errorf("rename through UpdateNode: type_group %q, want wordprocessing", got)
	}

	// A media-type change through UpdateNodeHeld with no lock held: the new type decides when the name has no known extension.
	must(s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, file("nod_mt", "untitled", "application/octet-stream")) }))
	must(s.Write(ctx, func(tx *Tx) error {
		n, err := getNode(ctx, tx.q, "acc_a", "nod_mt")
		if err != nil {
			return err
		}
		n.MimeType = "video/mp4"
		return tx.UpdateNodeHeld(ctx, n, "")
	}))
	if got := typeGroupOf(t, s, "nod_mt"); got != "video" {
		t.Errorf("media-type change through UpdateNodeHeld: type_group %q, want video", got)
	}
}

// TestTypeGroupRawWriters: the two writers that set a name without UpdateNode (the copy's publish and the staged placement)
// compute the group from the row's kind and type and the new name, in the same statement.
func TestTypeGroupRawWriters(t *testing.T) {
	ctx := context.Background()
	fx := newCopyFixture(t)

	// The copy's root is made a file, so the publish names a file: its group must follow the new name.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(fx.s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.CopyJobOf(ctx, "job_1")
		if err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `UPDATE nodes SET kind = 'file', mime_type = 'application/octet-stream' WHERE id = ?`, cur.RootID); err != nil {
			return err
		}
		if _, _, err := tx.StagedCopyBatch(ctx, cur, 10, true, "key_1", Now()); err != nil {
			return err
		}
		return tx.PublishStaged(ctx, cur, "fld_a", "nod_root", "copy.pdf", "copy.pdf", Now())
	}))
	var publishedGroup string
	if err := fx.s.r.QueryRowContext(ctx, `SELECT type_group FROM nodes WHERE id = (SELECT root_id FROM copy_jobs WHERE id = 'job_1')`).Scan(&publishedGroup); err != nil {
		t.Fatal(err)
	}
	if publishedGroup != "pdf" {
		t.Errorf("publish (copy root named copy.pdf): type_group %q, want pdf", publishedGroup)
	}

	// A staged file placed under a new name by SetStagedParent.
	s, _ := searchFixture(t)
	must(s.Write(ctx, func(tx *Tx) error {
		if err := tx.CreateStagingFolder(ctx, "acc_a", "fld_stage", "nod_stage_root", Now()); err != nil {
			return err
		}
		n := node("nod_st", "fld_stage", "nod_stage_root", "x.bin", "file")
		n.MimeType = "application/octet-stream"
		return tx.InsertNode(ctx, n)
	}))
	must(s.Write(ctx, func(tx *Tx) error {
		return tx.SetStagedParent(ctx, "acc_a", "fld_stage", "nod_st", "nod_root", "report.docx", "report.docx", Now())
	}))
	if got := typeGroupOf(t, s, "nod_st"); got != "wordprocessing" {
		t.Errorf("staged placement under report.docx: type_group %q, want wordprocessing", got)
	}
}
