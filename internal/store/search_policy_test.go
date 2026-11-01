// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// The text policy (review S3, decision 46): text is kept only for blobs a node holds, a cap is checked in the write that
// makes a text ready, and a cap on one account does not stop another.

// replaceBlob makes node nodeID hold blob (a new version of the file) in one write, as UpdateNodeHeld does.
func replaceBlob(t *testing.T, s *Store, nodeID, blobID string) {
	t.Helper()
	err := s.Write(context.Background(), func(tx *Tx) error {
		n := node(nodeID, "fld_a", "nod_root", nodeID+".txt", KindFile)
		n.BlobID = blobID
		n.Size = 10
		return tx.UpdateNodeHeld(context.Background(), n, "")
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestReplacedBlobLosesItsTextWhenNoNodeHoldsIt: an update that makes a new blob current drops the old blob's text, since
// only a version holds it now. A restore of the old content queues it again. Mutation: the drop in UpdateNodeHeld removed
// leaves the old row, and the first check fails.
func TestReplacedBlobLosesItsTextWhenNoNodeHoldsIt(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_old", "acc_a", 10)
	addFileNode(t, s, "nod_v", "blb_old")
	readyText(t, s, "blb_old", "acc_a", "oldwords")
	addBlob(t, s, "blb_new", "acc_a", 10)
	replaceBlob(t, s, "nod_v", "blb_new")
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts WHERE blob_id = 'blb_old'"); n != 0 {
		t.Fatalf("the replaced blob kept %d content rows; a version alone must not keep text", n)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts WHERE text LIKE '%oldwords%'"); n != 0 {
		t.Fatalf("the replaced blob's words are still indexed")
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts WHERE blob_id = 'blb_new' AND state = 'pending'"); n != 1 {
		t.Fatalf("the new blob has %d pending rows, want 1", n)
	}
	// A restore of the old content is the normal queue again: the node holds the blob, so its row is queued.
	replaceBlob(t, s, "nod_v", "blb_old")
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts WHERE blob_id = 'blb_old' AND state = 'pending'"); n != 1 {
		t.Fatalf("a restored blob has %d pending rows, want 1", n)
	}
}

// TestFinishTextDiscardsAVersionOnlyBlob: a result for a blob that only a version holds is not kept: the row goes.
// Mutation: FinishText checking versions as well as nodes keeps the row.
func TestFinishTextDiscardsAVersionOnlyBlob(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_v", "acc_a", 10)
	addFileNode(t, s, "nod_w", "blb_v")
	ctx := context.Background()
	var id int64
	if err := s.r.QueryRowContext(ctx, "SELECT id FROM search_texts WHERE blob_id = 'blb_v'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	// The node stops holding the blob, and a version keeps it, with no text drop in between: the worker had read it.
	if _, err := s.w.ExecContext(ctx, `INSERT INTO versions (id, account_id, node_id, blob_id, size, sha256, etag, mime_type,
		created_at, created_by, replaced_at, replaced_by, keep_forever) VALUES ('ver_1', 'acc_a', 'nod_w', 'blb_v', 10, 'x', '',
		'text/plain', 0, 'usr_none', 0, 'usr_none', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, "UPDATE nodes SET blob_id = NULL WHERE id = 'nod_w'"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishText(ctx, TextResult{ID: id, AccountID: "acc_a", BlobID: "blb_v", State: TextReady, Source: "plain",
		Text: "versionword", Bytes: 11}); err != nil {
		t.Fatal(err)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_texts WHERE blob_id = 'blb_v'"); n != 0 {
		t.Fatalf("a version-only blob kept its result: %d rows", n)
	}
}

// TestDropUnusedTextIsTheSweepsCatchAll: a node purged while a version still held its blob leaves text no node holds; the
// sweep's check drops it. Mutation: DropUnusedText does nothing, and the text stays.
func TestDropUnusedTextIsTheSweepsCatchAll(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_p", "acc_a", 10)
	addFileNode(t, s, "nod_p", "blb_p")
	readyText(t, s, "blb_p", "acc_a", "purgedword")
	if _, err := s.w.ExecContext(context.Background(), "DELETE FROM nodes WHERE id = 'nod_p'"); err != nil {
		t.Fatal(err)
	}
	n, err := s.DropUnusedText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts") != 0 {
		t.Fatalf("dropped %d rows; the purged file's words stayed: fts rows %d", n, scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"))
	}
}

// addAccount adds an account with the given ID.
func addAccount(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.w.ExecContext(context.Background(), `INSERT INTO accounts (id, name, status, created_at, updated_at)
		VALUES (?, ?, 'active', 0, 0)`, id, id); err != nil {
		t.Fatal(err)
	}
}

// addNodeOf adds a file node of account acc holding blob, in a folder and root of its own. The node is written with SQL:
// addFileNode would put it in acc_a.
func addNodeOf(t *testing.T, s *Store, acc, nodeID, blobID string) {
	t.Helper()
	ctx := context.Background()
	fld, root := "fld_"+acc, "nod_root_"+acc
	for _, q := range []string{
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('` + fld + `', '` + acc + `', 'F', 'f', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('` + root + `', '` + acc + `', '` + fld + `', NULL, '', '', 'dir', 0, 0)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, blob_id, size, created_at, modified_at)
		VALUES (?, ?, ?, ?, 'f.txt', 'f.txt', 'file', ?, 10, 0, 0)`, nodeID, acc, fld, root, blobID); err != nil {
		t.Fatal(err)
	}
	// The row is queued by the write that gives the node its blob, as InsertNodeNoIndex does.
	if _, err := s.w.ExecContext(ctx, `INSERT OR IGNORE INTO search_texts (blob_id, account_id, state)
		SELECT id, account_id, 'pending' FROM blobs WHERE id = ? AND account_id = ?`, blobID, acc); err != nil {
		t.Fatal(err)
	}
}

// readyWithCaps makes blob's row ready with n bytes of text, checked against the caps, as the worker's write does.
func readyWithCaps(t *testing.T, s *Store, blobID, acc string, n int64, perAccount, total int64) TextState {
	t.Helper()
	var id int64
	if err := s.r.QueryRowContext(context.Background(), "SELECT id FROM search_texts WHERE blob_id = ?", blobID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	text := make([]byte, n)
	for i := range text {
		text[i] = 'a'
	}
	if err := s.FinishText(context.Background(), TextResult{ID: id, AccountID: acc, BlobID: blobID, State: TextReady,
		Source: "plain", Text: string(text), Bytes: n, CapAccount: perAccount, CapTotal: total}); err != nil {
		t.Fatal(err)
	}
	var st TextState
	if err := s.r.QueryRowContext(context.Background(), "SELECT state, error FROM search_texts WHERE id = ?", id).Scan(&st.State, &st.Error); err != nil {
		t.Fatal(err)
	}
	return st
}

// TextState is the state and reason of one content row, in the tests.
type TextState struct{ State, Error string }

// TestPerAccountCapStopsOnlyTheFullAccount (review S3): an account at its cap does not stop another account's text. Two
// accounts, the first over its cap, the second not. Mutation: the cap checked over the installation instead of the account
// stops the second account too.
func TestPerAccountCapStopsOnlyTheFullAccount(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addAccount(t, s, "acc_b")
	addBlob(t, s, "blb_a1", "acc_a", 10)
	addFileNode(t, s, "nod_a1", "blb_a1")
	addBlob(t, s, "blb_a2", "acc_a", 10)
	addFileNode(t, s, "nod_a2", "blb_a2")
	addBlob(t, s, "blb_b1", "acc_b", 10)
	addNodeOf(t, s, "acc_b", "nod_b1", "blb_b1")
	const perAccount = 100
	if st := readyWithCaps(t, s, "blb_a1", "acc_a", 80, perAccount, 0); st.State != "ready" {
		t.Fatalf("first file of acc_a: %+v, want ready", st)
	}
	if st := readyWithCaps(t, s, "blb_a2", "acc_a", 30, perAccount, 0); st.State != "skipped" || st.Error != TextErrAccountFull {
		t.Fatalf("second file of acc_a: %+v, want skipped for the account's cap", st)
	}
	if st := readyWithCaps(t, s, "blb_b1", "acc_b", 30, perAccount, 0); st.State != "ready" {
		t.Fatalf("the other account was stopped by acc_a's cap: %+v", st)
	}
	if n := scalarCount(t, s, "SELECT COUNT(*) FROM search_text_fts"); n != 2 {
		t.Fatalf("full-text rows %d, want 2 (the two ready files)", n)
	}
	if got := scalarCount(t, s, "SELECT bytes FROM search_text_usage WHERE account_id = 'acc_a'"); got != 80 {
		t.Fatalf("acc_a usage %d, want 80", got)
	}
}

// TestInstallationCapStopsEveryAccount: the installation's cap holds across accounts. Mutation: the total not checked.
func TestInstallationCapStopsEveryAccount(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addAccount(t, s, "acc_b")
	addBlob(t, s, "blb_t1", "acc_a", 10)
	addFileNode(t, s, "nod_t1", "blb_t1")
	addBlob(t, s, "blb_t2", "acc_b", 10)
	addNodeOf(t, s, "acc_b", "nod_t2", "blb_t2")
	if st := readyWithCaps(t, s, "blb_t1", "acc_a", 70, 0, 100); st.State != "ready" {
		t.Fatalf("first file: %+v", st)
	}
	if st := readyWithCaps(t, s, "blb_t2", "acc_b", 40, 0, 100); st.State != "skipped" || st.Error != TextErrInstallFull {
		t.Fatalf("second file over the installation's cap: %+v", st)
	}
}

// TestCappedRowsWaitForRoom: a row skipped for a cap is released by the sweep once the account has room for one more file,
// and not before. Mutation: the release ignores the room and releases at once.
func TestCappedRowsWaitForRoom(t *testing.T) {
	t.Parallel()
	s, _ := searchFixture(t)
	addBlob(t, s, "blb_c1", "acc_a", 10)
	addFileNode(t, s, "nod_c1", "blb_c1")
	addBlob(t, s, "blb_c2", "acc_a", 10)
	addFileNode(t, s, "nod_c2", "blb_c2")
	if st := readyWithCaps(t, s, "blb_c1", "acc_a", 90, 100, 0); st.State != "ready" {
		t.Fatalf("first file: %+v", st)
	}
	if st := readyWithCaps(t, s, "blb_c2", "acc_a", 15, 100, 0); st.State != "skipped" {
		t.Fatalf("second file (90 + 15 > 100): %+v, want skipped", st)
	}
	// One more file of 20 bytes would not fit (90 + 20 > 100): nothing is released.
	if n, err := s.ReleaseTextCapped(context.Background(), 20, 100, 0); err != nil || n != 0 {
		t.Fatalf("released %d with no room for a file of 20 bytes (err %v)", n, err)
	}
	// The first file goes (its node is purged, and the sweep drops its text): 0 + 20 fits, and the capped row is due again.
	if _, err := s.w.ExecContext(context.Background(), "DELETE FROM nodes WHERE id = 'nod_c1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DropUnusedText(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReleaseTextCapped(context.Background(), 20, 100, 0); err != nil || n != 1 {
		t.Fatalf("released %d once the account has room (err %v), want 1", n, err)
	}
}
