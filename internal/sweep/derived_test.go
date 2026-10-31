// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// derivedFixture is a store, blobs, and a derived cache with one account.
type derivedFixture struct {
	t     *testing.T
	ctx   context.Context
	st    *store.Store
	blobs *blob.Dir
	cache *derived.Cache
	acc   string
}

func newDerivedFixture(t *testing.T, maxBytes int64) *derivedFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, "test.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := derived.NewFiles(filepath.Join(dir, "derived"))
	if err != nil {
		t.Fatal(err)
	}
	f := &derivedFixture{t: t, ctx: ctx, st: st, blobs: blobs,
		cache: derived.NewCache(st, files, maxBytes, nil), acc: id.New("acc")}
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertAccount(ctx, &store.Account{ID: f.acc, Name: "Acme", Status: store.AccountActive,
			CreatedAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// blob commits contents as a blob of the account, in the given state.
func (f *derivedFixture) blob(content []byte, state string, after *time.Time) string {
	f.t.Helper()
	w, err := f.blobs.Create(f.ctx, f.acc, 1<<20)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		f.t.Fatal(err)
	}
	c, err := w.Commit()
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		return tx.InsertBlob(f.ctx, &store.Blob{ID: c.ID, AccountID: f.acc, Size: c.Size, SHA256: c.SHA256,
			State: state, DeleteAfter: after, CreatedAt: time.Now()})
	}); err != nil {
		f.t.Fatal(err)
	}
	return c.ID
}

// ready makes a ready derived row of a blob, with a file of the given size, last used at used.
func (f *derivedFixture) ready(blobID string, size int, used time.Time) *store.Derived {
	f.t.Helper()
	d := &store.Derived{ID: id.New("drv"), AccountID: f.acc, BlobID: blobID, Kind: derived.Kind(256),
		Converter: derived.EncoderVersion + "+test", CreatedAt: used}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		_, err := tx.InsertDerivedPending(f.ctx, d)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	path, err := f.cache.Files().Path(f.acc, d.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, size), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		_, err := tx.CompleteDerived(f.ctx, f.acc, d.ID, int64(size), "", used)
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	return d
}

func (f *derivedFixture) fileExists(d *store.Derived) bool {
	f.t.Helper()
	path, _ := f.cache.Files().Path(f.acc, d.ID)
	_, err := os.Stat(path)
	return err == nil
}

func (f *derivedFixture) row(d *store.Derived) bool {
	f.t.Helper()
	_, err := f.st.DerivedFor(f.ctx, f.acc, d.BlobID, d.Kind, d.Converter)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return true
}

// TestSweepRemovesDerivedFilesWithTheirBlob: an orphan blob past its grace takes its thumbnails, rows and files.
func TestSweepRemovesDerivedFilesWithTheirBlob(t *testing.T) {
	t.Parallel()
	f := newDerivedFixture(t, 1<<40)
	past := time.Now().Add(-time.Hour)
	bl := f.blob([]byte("contents"), store.BlobOrphan, &past)
	d := f.ready(bl, 100, time.Now())
	sw := New(Options{Store: f.st, Blobs: f.blobs, Derived: f.cache, DeleteGrace: time.Hour})
	res, err := sw.Once(f.ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Errorf("removed %d blobs, want 1", res.Removed)
	}
	if f.row(d) {
		t.Error("the derived row survived its blob")
	}
	if f.fileExists(d) {
		t.Error("the derived file survived its blob")
	}
}

// TestSweepEvictsLeastRecentlyUsedOverBudget: past the budget, the least recently used ready files go, and the rest stay.
func TestSweepEvictsLeastRecentlyUsedOverBudget(t *testing.T) {
	t.Parallel()
	f := newDerivedFixture(t, 250)
	old := f.ready(f.blob([]byte("a"), store.BlobLive, nil), 100, time.Now().Add(-2*time.Hour))
	mid := f.ready(f.blob([]byte("b"), store.BlobLive, nil), 100, time.Now().Add(-time.Hour))
	newest := f.ready(f.blob([]byte("c"), store.BlobLive, nil), 100, time.Now())
	sw := New(Options{Store: f.st, Blobs: f.blobs, Derived: f.cache, DeleteGrace: time.Hour})
	res, err := sw.Once(f.ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.DerivedEvicted != 1 {
		t.Errorf("evicted %d, want 1", res.DerivedEvicted)
	}
	if f.row(old) || f.fileExists(old) {
		t.Error("the least recently used file was kept")
	}
	if !f.row(mid) || !f.row(newest) {
		t.Error("a file within the budget was removed")
	}
}

// TestSweepRemovesAbandonedTemporaryFiles: a temporary file an hour old is a worker's leftover, not a live decode.
func TestSweepRemovesAbandonedTemporaryFiles(t *testing.T) {
	t.Parallel()
	f := newDerivedFixture(t, 1<<40)
	tmp, _ := f.cache.Files().TmpPath(id.New("drv"))
	if err := os.WriteFile(tmp, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(tmp, old, old); err != nil {
		t.Fatal(err)
	}
	sw := New(Options{Store: f.st, Blobs: f.blobs, Derived: f.cache, DeleteGrace: time.Hour})
	res, err := sw.Once(f.ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.DerivedTemp != 1 {
		t.Errorf("removed %d temporary files, want 1", res.DerivedTemp)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Error("the temporary file survived")
	}
}
