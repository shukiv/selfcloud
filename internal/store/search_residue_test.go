// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Residue of deleted text. A deleted file's words must not be in the database file, or in a copy made with
// VACUUM INTO, once the write-ahead log is checkpointed. Each test has a control: the same marker held by a live file is
// found in the file, so a scan that finds nothing is known to be able to find something.

const (
	residueMarker  = "zqxresiduemarker"
	residueControl = "zqxlivecontrol"
)

// residueFixture is a store with one live file holding residueControl and one file holding residueMarker, whose node
// is then deleted and its blob orphaned, as a purge does.
func residueFixture(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	s, path := searchFixture(t)
	addBlob(t, s, "blb_live", "acc_a", 10)
	addFileNode(t, s, "nod_live", "blb_live")
	readyText(t, s, "blb_live", "acc_a", residueControl+" kept words")
	addBlob(t, s, "blb_gone", "acc_a", 10)
	addFileNode(t, s, "nod_gone", "blb_gone")
	readyText(t, s, "blb_gone", "acc_a", residueMarker+" deleted words")
	if _, err := s.w.ExecContext(ctx, "DELETE FROM nodes WHERE id = 'nod_gone'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, "UPDATE blobs SET state = 'orphan', delete_after = 0 WHERE id = 'blb_gone'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.DeleteOrphan(ctx, "acc_a", "blb_gone")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return s, path
}

// fileHolds reports whether the file at path holds the bytes of word.
func fileHolds(t *testing.T, path, word string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(b, []byte(word))
}

// TestDeletedTextLeavesNoBytesInTheDatabaseFile: after the deletion and a checkpoint that truncates the log, the words
// of the deleted file are not in the database file. Without secure delete the pages keep them.
func TestDeletedTextLeavesNoBytesInTheDatabaseFile(t *testing.T) {
	t.Parallel()
	s, path := residueFixture(t)
	if _, err := s.w.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if !fileHolds(t, path, residueControl) {
		t.Fatal("control: the live file's words are not in the database file, so the scan is blind")
	}
	if fileHolds(t, path, residueMarker) {
		t.Fatal("the deleted file's words are still in the database file after the checkpoint")
	}
}

// TestVacuumIntoCopyHoldsNoDeletedText: a backup made with VACUUM INTO carries the live text and none of the deleted.
func TestVacuumIntoCopyHoldsNoDeletedText(t *testing.T) {
	t.Parallel()
	s, _ := residueFixture(t)
	dst := filepath.Join(t.TempDir(), "backup.db")
	if _, err := s.w.ExecContext(context.Background(), "VACUUM INTO '"+dst+"'"); err != nil {
		t.Fatal(err)
	}
	if !fileHolds(t, dst, residueControl) {
		t.Fatal("control: the copy does not hold the live file's words")
	}
	if fileHolds(t, dst, residueMarker) {
		t.Fatal("VACUUM INTO copied the deleted file's words")
	}
}

// TestCheckpointTakesDeletedTextOutOfTheLog (decision 46): the deleted words sit in the write-ahead log until a
// checkpoint. A passive checkpoint leaves them in the -wal file; the sweep's truncating checkpoint takes them out of both
// files. The marker is in the log before the checkpoint, so the test is not passing for want of the bytes.
func TestCheckpointTakesDeletedTextOutOfTheLog(t *testing.T) {
	t.Parallel()
	s, path := residueFixture(t)
	if !fileHolds(t, path+"-wal", residueMarker) {
		t.Fatal("precondition: the deleted words are not in the -wal file before the checkpoint, so the test proves nothing")
	}
	busy, err := s.Checkpoint(context.Background())
	if err != nil || busy {
		t.Fatalf("checkpoint: busy %v, err %v", busy, err)
	}
	if fileHolds(t, path, residueMarker) {
		t.Error("the deleted words are in the database file after the checkpoint")
	}
	if fileHolds(t, path+"-wal", residueMarker) {
		t.Error("the deleted words are in the -wal file after the checkpoint")
	}
}

// TestCheckpointWithAReaderIsBusyNotAnError: a reader that holds the log stops the truncation. That is reported as busy,
// and the next pass tries again; it is not an error of the sweep.
func TestCheckpointWithAReaderIsBusyNotAnError(t *testing.T) {
	t.Parallel()
	s, _ := residueFixture(t)
	ctx := context.Background()
	rows, err := s.r.QueryContext(ctx, "SELECT COUNT(*) FROM search_text_fts")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() { // the statement is stepped, so its read snapshot is open
		t.Fatal("no row from the reader")
	}
	busy, err := s.Checkpoint(ctx)
	if err != nil {
		t.Fatalf("a busy checkpoint returned an error: %v", err)
	}
	if !busy {
		t.Error("a checkpoint with a reader open was not reported busy")
	}
}
