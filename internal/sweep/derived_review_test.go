// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/derived"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// L3: one ready file that cannot be removed is logged, and the rest of the pass still runs.
// The file is replaced by a non-empty directory, which os.Remove refuses with an error other than not-exist.
func TestUnremovableDerivedFileDoesNotStopLaterSteps(t *testing.T) {
	t.Parallel()
	f := newDerivedFixture(t, 10) // a 10-byte budget: the 100-byte file is over it, so eviction tries to remove it
	stuck := f.ready(f.blob([]byte("a"), store.BlobLive, nil), 100, time.Now().Add(-2*time.Hour))
	path, err := f.cache.Files().Path(f.acc, stuck.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A failed row older than the sweep's age: a step after eviction must still clear it.
	failedBlob := f.blob([]byte("b"), store.BlobLive, nil)
	pending := &store.Derived{ID: id.New("drv"), AccountID: f.acc, BlobID: failedBlob, Kind: derived.Kind(256),
		Converter: derived.EncoderVersion + "+test", CreatedAt: time.Now()}
	old := time.Now().Add(-31 * 24 * time.Hour)
	if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
		if _, err := tx.InsertDerivedPending(f.ctx, pending); err != nil {
			return err
		}
		_, err := tx.FailDerived(f.ctx, f.acc, pending.ID, "old", old, 1, time.Time{})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sw := New(Options{Store: f.st, Blobs: f.blobs, Derived: f.cache, DeleteGrace: time.Hour})
	res, err := sw.Once(f.ctx, time.Now())
	if err != nil {
		t.Fatalf("a pass stopped by one derived file: %v", err)
	}
	if res.Failed == 0 {
		t.Error("the refused removal was not counted")
	}
	if res.DerivedCleared != 1 {
		t.Errorf("failed rows cleared = %d, want 1: a later step did not run", res.DerivedCleared)
	}
}

// L4: a derived file no row names is removed once it is an hour old. A young one is kept for the next pass, and an
// old one with a row is kept whatever its state.
func TestOrphanDerivedFilesAreRemovedWhenOldAndUnnamed(t *testing.T) {
	t.Parallel()
	f := newDerivedFixture(t, 1<<40)
	orphan := id.New("drv")
	young := id.New("drv")
	named := f.ready(f.blob([]byte("c"), store.BlobLive, nil), 10, time.Now().Add(-2*time.Hour))
	write := func(drvID string, age time.Duration) string {
		p, err := f.cache.Files().Path(f.acc, drvID)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
		return p
	}
	orphanPath := write(orphan, 2*time.Hour)
	youngPath := write(young, time.Minute)
	namedPath, _ := f.cache.Files().Path(f.acc, named.ID)
	if err := os.Chtimes(namedPath, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	sw := New(Options{Store: f.st, Blobs: f.blobs, Derived: f.cache, DeleteGrace: time.Hour})
	res, err := sw.Once(f.ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.DerivedOrphans != 1 {
		t.Errorf("orphans removed = %d, want 1", res.DerivedOrphans)
	}
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("the old orphan survived")
	}
	if _, err := os.Stat(youngPath); err != nil {
		t.Error("a file made within the hour was removed as an orphan")
	}
	if _, err := os.Stat(namedPath); err != nil {
		t.Error("a file with a row was removed as an orphan")
	}
}

// M1: a failed row is cleared after 30 days, and kept before that.
func TestFailedDerivedRowsAreClearedAfterThirtyDays(t *testing.T) {
	t.Parallel()
	f := newDerivedFixture(t, 1<<40)
	mk := func(failedAt time.Time) *store.Derived {
		d := &store.Derived{ID: id.New("drv"), AccountID: f.acc, BlobID: f.blob([]byte(failedAt.String()), store.BlobLive, nil),
			Kind: derived.Kind(256), Converter: derived.EncoderVersion + "+test", CreatedAt: failedAt}
		if err := f.st.Write(f.ctx, func(tx *store.Tx) error {
			if _, err := tx.InsertDerivedPending(f.ctx, d); err != nil {
				return err
			}
			_, err := tx.FailDerived(f.ctx, f.acc, d.ID, "x", failedAt, 1, time.Time{})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return d
	}
	now := time.Now()
	old := mk(now.Add(-31 * 24 * time.Hour))
	recent := mk(now.Add(-29 * 24 * time.Hour))
	sw := New(Options{Store: f.st, Blobs: f.blobs, Derived: f.cache, DeleteGrace: time.Hour})
	if _, err := sw.Once(f.ctx, now); err != nil {
		t.Fatal(err)
	}
	if f.row(old) {
		t.Error("a failure of 31 days was kept")
	}
	if !f.row(recent) {
		t.Error("a failure of 29 days was cleared")
	}
}
