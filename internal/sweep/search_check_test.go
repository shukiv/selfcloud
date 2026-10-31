// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestSweepRepairsMissingSearchRowsAtItsCheck: a node written with no index and no queue has no search row. The sweep's
// check finds it, the repair queues it, and the search job makes the row. A second pass within the check interval does
// not check again.
func TestSweepRepairsMissingSearchRowsAtItsCheck(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "selfcloud.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc := &store.Account{ID: id.New("acc"), Name: "A", Status: store.AccountActive, CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertAccount(ctx, acc) }); err != nil {
		t.Fatal(err)
	}
	fo := &store.Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "Docs", NameKey: "docs",
		CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertFolder(ctx, fo, "usr_test") }); err != nil {
		t.Fatal(err)
	}
	root, err := st.RootNode(ctx, acc.ID, fo.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := store.Now()
	n := &store.Node{ID: id.New("nod"), AccountID: acc.ID, FolderID: fo.ID, ParentID: root.ID, Name: "lost.txt",
		NameKey: "lost.txt", Kind: store.KindFile, Size: 1, CreatedAt: now, ModifiedAt: now, CreatedBy: "usr_test",
		ModifiedBy: "usr_test", OwnerID: "usr_test"}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertNodeNoIndex(ctx, n) }); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	sw := New(Options{Store: st, Blobs: blobs, DeleteGrace: time.Hour, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	res, err := sw.Once(ctx, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !res.SearchRepaired {
		t.Fatalf("sweep did not repair the missing search row; log:\n%s", logs.String())
	}
	if left, err := st.SearchBacklogLeft(ctx); err != nil || left != 1 {
		t.Fatalf("backlog = %d, %v after the repair; want the folder queued", left, err)
	}
	for {
		done, err := st.SearchIndexStep(ctx, store.SearchBatch)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	// The next pass is within the interval, so it does not check again.
	res, err = sw.Once(ctx, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.SearchRepaired {
		t.Fatal("the second pass repaired again inside the check interval")
	}
}
