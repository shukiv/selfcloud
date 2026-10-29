// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestRetentionSkipsARootRestoredAndTrashedAgain: the sweep lists a root that has been in the trash past the
// retention. Before it purges, the root is restored and trashed again, so its trashed_at is fresh. The purge
// step must not take it, because the row no longer has the age the listing saw. A root still past the
// retention, in the same listing, is purged.
func TestRetentionSkipsARootRestoredAndTrashedAgain(t *testing.T) {
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
	old := store.Now().Add(-40 * 24 * time.Hour)
	insert := func(name string) string {
		n := &store.Node{ID: id.New("nod"), AccountID: acc.ID, FolderID: folder.ID, ParentID: root.ID, Name: name, NameKey: name,
			Kind: store.KindFile, CreatedAt: old, ModifiedAt: old, CreatedBy: "usr_test", ModifiedBy: "usr_test"}
		if err := st.Write(ctx, func(tx *store.Tx) error {
			if err := tx.InsertNode(ctx, n); err != nil {
				return err
			}
			return tx.TrashNode(ctx, acc.ID, n.ID, "usr_test", old)
		}); err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	churned, control := insert("churned.txt"), insert("control.txt")

	now := store.Now()
	due, err := st.ExpiredTrash(ctx, now.Add(-30*24*time.Hour), 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("listing = %d roots, %v; want both past the retention", len(due), err)
	}
	// Between the listing and the purge: the first root is restored and trashed again just now.
	if err := st.Write(ctx, func(tx *store.Tx) error {
		if err := tx.Untrash(ctx, acc.ID, churned); err != nil {
			return err
		}
		return tx.TrashNode(ctx, acc.ID, churned, "usr_test", now)
	}); err != nil {
		t.Fatal(err)
	}

	sw := New(Options{Store: st, Blobs: blobs, TrashRetention: 30 * 24 * time.Hour})
	for _, n := range due {
		err := sw.purgeTrashRoot(ctx, n, now)
		switch n.ID {
		case churned:
			if !errors.Is(err, store.ErrNotFound) {
				t.Errorf("purge of the re-trashed root = %v, want ErrNotFound (it must survive)", err)
			}
		case control:
			if err != nil {
				t.Errorf("purge of the root still past the retention = %v, want nil", err)
			}
		}
	}
	if _, err := st.Node(ctx, acc.ID, churned); err != nil {
		t.Errorf("the re-trashed root is gone: %v", err)
	}
	if _, err := st.Node(ctx, acc.ID, control); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the control root survived the purge: %v", err)
	}
}
