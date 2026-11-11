// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestRetentionNowIsReadEachPass: with RetentionNow set, each pass reads the trash retention it gives, so an item kept by
// one pass is purged by the next once the retention is lowered, with no new sweeper. The fixed TrashRetention is not
// used.
func TestRetentionNowIsReadEachPass(t *testing.T) {
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
	folder := &store.Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "Docs", NameKey: "docs",
		CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAccount(ctx, acc); err != nil {
			return err
		}
		return tx.InsertFolder(ctx, folder, "usr_test")
	}); err != nil {
		t.Fatal(err)
	}
	root, err := st.RootNode(ctx, acc.ID, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	trashed := store.Now().Add(-2 * 24 * time.Hour)
	n := &store.Node{ID: id.New("nod"), AccountID: acc.ID, FolderID: folder.ID, ParentID: root.ID, Name: "a.txt",
		NameKey: "a.txt", Kind: store.KindFile, CreatedAt: trashed, ModifiedAt: trashed, CreatedBy: "usr_test",
		ModifiedBy: "usr_test"}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertNode(ctx, n); err != nil {
			return err
		}
		return tx.TrashNode(ctx, acc.ID, n.ID, "usr_test", trashed)
	}); err != nil {
		t.Fatal(err)
	}

	var keep atomic.Int64
	keep.Store(int64(30 * 24 * time.Hour))
	sw := New(Options{Store: st, Blobs: blobs, Interval: time.Hour, TrashRetention: time.Hour,
		RetentionNow: func() Retention { return Retention{Trash: time.Duration(keep.Load())} }})
	res, err := sw.Once(ctx, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Purged != 0 {
		t.Fatalf("a pass with 30 days of retention purged %d items two days in the trash", res.Purged)
	}
	keep.Store(int64(24 * time.Hour))
	if res, err = sw.Once(ctx, store.Now()); err != nil {
		t.Fatal(err)
	}
	if res.Purged != 1 {
		t.Errorf("a pass after the retention went down to a day purged %d items, want 1", res.Purged)
	}
}
