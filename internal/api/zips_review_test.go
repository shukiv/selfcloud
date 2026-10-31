// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestZipLongExtensionCollisionDoesNotPanic: two root names that fold to one key, each 255 bytes with a 254-byte
// extension. The second one takes a suffix, and the name must still fit in 255 bytes. The arithmetic used to go
// negative here and panicked (review of commit 1a7a512, defect 1).
func TestZipLongExtensionCollisionDoesNotPanic(t *testing.T) {
	ns := nameSet{}
	long := "." + strings.Repeat("b", 253)
	first := "a" + long
	if got := ns.unique(first); got != first {
		t.Fatalf("first = %q", got)
	}
	got := ns.unique("A" + long)
	if len(got) > 255 || !utf8.ValidString(got) {
		t.Errorf("collision name is %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if !strings.Contains(got, " (1)") {
		t.Errorf("collision name %q has no suffix", got)
	}
	if foldKey(got) == foldKey(first) {
		t.Errorf("collision name %q is not distinct", got)
	}
	// A stem that has room keeps its whole extension.
	if got := fitName("report", ".txt", " (1)"); got != "report (1).txt" {
		t.Errorf("fitName with room = %q", got)
	}
}

// TestZipAccountDiskShare: one account's stored zips may hold its share of zip.max_disk and no more, even when the
// installation has room. Another account is not affected (review defect 2; zip.max_disk_per_account).
func TestZipAccountDiskShare(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) {
		c.Zip.MaxBytes = 10 << 20
		c.Zip.MaxDisk = 10 << 20
		c.Zip.MaxDiskPerAccount = 1 << 20
	})
	big := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/big")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/big/one.bin", incompressible(600<<10))
	first := createZip(t, fx, nil, big.ID)
	if j := runZip(t, fx, first.ID); j.Status != "succeeded" {
		t.Fatalf("first zip %s: %s", j.Status, j.Error)
	}
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/two.bin", incompressible(600<<10))
	second := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, second.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "disk share") {
		t.Fatalf("second zip %s %q, want failed on the account's disk share", j.Status, j.Error)
	}
	// The other account's zip is made: the share is per account.
	acc2, key2 := newAccount(t, fx.s, fx.admin, "Other")
	f2 := newFolder(t, fx.s, key2, acc2.ID, "Team")
	y := putNode(t, fx.s, key2, f2.ID, "/y.bin", incompressible(600<<10))
	rec := call(t, fx.s, "POST", "/api/v1/zips", key2, map[string]any{"nodes": []string{y.ID}})
	wantStatus(t, rec, http.StatusAccepted)
	other := decode[jobJSON](t, rec)
	if j := runZip(t, fx, other.ID); j.Status != "succeeded" {
		t.Fatalf("other account's zip %s: %s", j.Status, j.Error)
	}
}

// TestZipFailedRowsAreSwept: a failed zip is kept for zip.ttl after its creation, and then its row and roots are
// removed by the sweep (review defect 4).
func TestZipFailedRowsAreSwept(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) { c.Zip.MaxEntries = 3 })
	job := createZip(t, fx, nil, fx.a.ID)
	if j := runZip(t, fx, job.ID); j.Status != "failed" {
		t.Fatalf("job %s, want failed on the entry limit", j.Status)
	}
	ctx := context.Background()
	res, err := fx.s.zipFiles.Sweep(ctx, store.Now(), 100)
	if err != nil || res.Failed != 0 {
		t.Fatalf("sweep at once %+v %v: a fresh failed zip must be kept", res, err)
	}
	if _, err := fx.s.store.Zip(ctx, fx.acc.ID, job.Target); err != nil {
		t.Fatalf("fresh failed zip removed: %v", err)
	}
	later := store.Now().Add(fx.s.cfg.Zip.TTL.D() + time.Minute)
	res, err = fx.s.zipFiles.Sweep(ctx, later, 100)
	if err != nil || res.Failed != 1 {
		t.Fatalf("sweep after the ttl %+v %v, want one failed row removed", res, err)
	}
	if _, err := fx.s.store.Zip(ctx, fx.acc.ID, job.Target); err != store.ErrNotFound {
		t.Errorf("failed zip after the ttl: %v, want not found", err)
	}
}

// TestZipOrphanFilesAreSwept: a zip file that no row names is removed once it is an hour old by its modification
// time. A young one is kept, so a file being published is not caught (review defect 5).
func TestZipOrphanFilesAreSwept(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	ctx := context.Background()
	path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
	// The row goes and the file stays: what a crash between the two of a delete leaves.
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		_, err := tx.DeleteZip(ctx, fx.acc.ID, job.Target)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	old, _ := fx.s.zipFiles.Path(fx.acc.ID, id.New("zip"))
	young, _ := fx.s.zipFiles.Path(fx.acc.ID, id.New("zip"))
	for _, p := range []string{old, young} {
		if err := os.WriteFile(p, []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	res, err := fx.s.zipFiles.Sweep(ctx, time.Now(), 100)
	if err != nil || res.Orphans != 2 {
		t.Fatalf("sweep %+v %v, want the old orphan and the old file of the deleted row", res, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old orphan still there: %v", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("young orphan removed: %v", err)
	}
}

// TestZipWalkErrorFailsTheZip: an access error that is not a refusal (here, the store failing) fails the zip. It is
// not taken for "the caller cannot read this", which would leave the node out and say so only in the count (review
// defect 6).
func TestZipWalkErrorFailsTheZip(t *testing.T) {
	fx := zipFixture(t)
	fx.s.zipWalkFault = func(nodeID string) error {
		if nodeID == fx.sib.ID {
			return errors.New("the database is down")
		}
		return nil
	}
	job := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, job.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "database is down") {
		t.Fatalf("job %s %q, want failed on the store error", j.Status, j.Error)
	}
}

// TestZipLeftOutSubtreeCountsEveryNode: a folder the caller cannot read is left out with everything under it, and the
// header counts all of them (review gap: whole-subtree skip).
func TestZipLeftOutSubtreeCountsEveryNode(t *testing.T) {
	fx := zipFixture(t)
	fx.s.zipWalkFault = func(nodeID string) error {
		if nodeID == fx.c.ID {
			return apierr.New(apierr.Forbidden, "not for you")
		}
		return nil
	}
	job := createZip(t, fx, nil, fx.a.ID)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	rec := getZip(t, fx, nil, job.Target, nil)
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("X-Selfcloud-Skipped"); got != "2" {
		t.Errorf("skipped %q, want 2: the folder and the file under it", got)
	}
	got := zipEntries(t, rec.Body.Bytes())
	if _, ok := got["a/c/x.txt"]; ok {
		t.Errorf("a file under a left-out folder is in the zip")
	}
	if len(got) != 4 {
		t.Errorf("entries %v, want 4", got)
	}
}

// TestZipPanicReleasesTheWork: a panic in a turn releases what the job holds (the registry entry and the open file),
// fails the row, removes the partial file, and then goes on, so the runner records the job (review defect 7).
func TestZipPanicReleasesTheWork(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	stored, err := fx.s.store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.zipFault = func(stage string) error {
		if stage == "entry" {
			panic("boom")
		}
		return nil
	}
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("the panic did not go on to the runner: recovered %v", r)
			}
		}()
		_ = fx.s.runZipJob(context.Background(), stored)
	}()
	fx.s.zipFault = nil
	if fx.s.zips.get(job.Target) != nil {
		t.Error("the work is still registered after the panic")
	}
	z, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
	if err != nil || z.State != store.ZipFailed {
		t.Fatalf("zip after the panic: %v, state %v, want failed", err, z)
	}
	part, _ := fx.s.zipFiles.PartPath(fx.acc.ID, job.Target)
	if _, err := os.Stat(filepath.Clean(part)); !os.IsNotExist(err) {
		t.Errorf("partial file left after the panic: %v", err)
	}
}
