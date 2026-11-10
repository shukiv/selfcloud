// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestUploadRefusedWhileZipReservesDisk: a stored zip that reserved 4 GiB while it is pending holds that disk. An upload
// of 4 GiB on a disk with 5 GiB usable is refused as the disk rule refuses: storage_full, scope server (decision 120).
func TestUploadRefusedWhileZipReservesDisk(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) {
		c.Storage.MinFree = 50
		c.Upload.MaxDiskSharePercent = 100
	})
	fx.s.freeBytes = func(string) (uint64, error) { return 5<<30 + 50, nil }
	z := createZip(t, fx, nil, fx.a.ID)
	// The snapshot end of the zip job: its reservation is taken, as the job takes it.
	if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.SetZipSnapshot(context.Background(), z.Target, 4<<30, 1, 0, store.Now())
	}); err != nil {
		t.Fatal(err)
	}
	rec := createUpload(t, fx.s, fx.key, 4<<30, md("folder", fx.fld.ID, "name", "big.bin"))
	wantError(t, rec, http.StatusInsufficientStorage, "storage_full")
	if d := decodeErrorDetails(t, rec); d["scope"] != "server" || d["reason"] != "disk" {
		t.Errorf("details scope %v reason %v, want server and disk", d["scope"], d["reason"])
	}
}

// TestZipRefusedWhileUploadReservesDisk: an upload holds 900 KiB of a 1 MiB disk. A stored zip of 600 KiB needs the
// same disk, so its start is refused: the job fails storage_full with reason disk (decision 120).
func TestZipRefusedWhileUploadReservesDisk(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) {
		c.Storage.MinFree = 50
		c.Upload.MaxDiskSharePercent = 100
	})
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/big.bin", incompressible(600<<10))
	fx.s.freeBytes = func(string) (uint64, error) { return 1<<20 + 50, nil }
	uploadID(t, createUpload(t, fx.s, fx.key, 900<<10, md("folder", fx.fld.ID, "name", "held.bin")))
	z := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, z.ID)
	if j.Status != "failed" {
		t.Fatalf("zip %s, want failed on the disk", j.Status)
	}
	got := decode[jobJSON](t, rawCall(t, fx.s, "GET", "/api/v1/jobs/"+z.ID, fx.key, "", nil))
	if got.ErrorCode != "storage_full" || got.ErrorReason != "disk" {
		t.Errorf("job error code %q reason %q, want storage_full with disk", got.ErrorCode, got.ErrorReason)
	}
}

// TestJobReadableCurrentRuleUnchanged: jobReadable keeps today's rule for every kind. The maker and an admin key read a
// job; an account owner reads any job of its account; a member who did not queue it, a token of the maker, another
// account, and no caller at all do not.
func TestJobReadableCurrentRuleUnchanged(t *testing.T) {
	job := &store.Job{ID: "job_1", AccountID: "acc_a", Kind: jobZip, Target: "zip_1",
		Payload: fmt.Sprintf(`{"actor":{"kind":%q,"id":"usr_a"}}`, auth.KindUser)}
	cases := map[string]struct {
		p    *auth.Principal
		want bool
	}{
		"the maker":         {&auth.Principal{Kind: auth.KindUser, ID: "usr_a", AccountID: "acc_a", UserID: "usr_a", Role: "member"}, true},
		"an admin key":      {&auth.Principal{Kind: auth.KindAdminKey, ID: "key_admin"}, true},
		"the account owner": {&auth.Principal{Kind: auth.KindUser, ID: "usr_o", AccountID: "acc_a", UserID: "usr_o", Role: auth.RoleOwner}, true},
		"another member":    {&auth.Principal{Kind: auth.KindUser, ID: "usr_c", AccountID: "acc_a", UserID: "usr_c", Role: "member"}, false},
		"maker's token":     {&auth.Principal{Kind: auth.KindUser, ID: "usr_a", AccountID: "acc_a", UserID: "usr_a", Role: "member", Token: &auth.TokenScope{All: true}}, false},
		"another account":   {&auth.Principal{Kind: auth.KindAccountKey, ID: "key_b", AccountID: "acc_b"}, false},
		"no caller at all":  {nil, false},
	}
	for name, c := range cases {
		if got := jobReadable(job, c.p); got != c.want {
			t.Errorf("%s: jobReadable = %v, want %v", name, got, c.want)
		}
	}
}

// TestZipRestartNotRefusedByItsOwnReservation: a zip that crashed after its snapshot keeps its reservation (900 KiB here).
// The restarted job must not count that reservation against the disk again: a 600 KiB zip fits a 1 MiB disk.
func TestZipRestartNotRefusedByItsOwnReservation(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) {
		c.Storage.MinFree = 50
		c.Upload.MaxDiskSharePercent = 100
	})
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/big.bin", incompressible(600<<10))
	fx.s.freeBytes = func(string) (uint64, error) { return 1<<20 + 50, nil }
	z := createZip(t, fx, nil, fx.a.ID)
	// The first run took its reservation at its snapshot end, then stopped before publish.
	if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.SetZipSnapshot(context.Background(), z.Target, 900<<10, 1, 0, store.Now())
	}); err != nil {
		t.Fatal(err)
	}
	if j := runZip(t, fx, z.ID); j.Status != "succeeded" {
		t.Fatalf("restarted zip %s (%s), want succeeded: its own old reservation is not a claim on the disk", j.Status, j.Error)
	}
}

// TestZipStartCountsPieceBytesInFlight: an upload piece whose bytes are still arriving holds the disk in memory, before
// the database sees it. A zip start must count those bytes too. The disk is 1 MiB; 600 KiB of piece bytes
// are in flight; a 600 KiB zip is refused.
func TestZipStartCountsPieceBytesInFlight(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) {
		c.Storage.MinFree = 50
		c.Upload.MaxDiskSharePercent = 100
	})
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/big.bin", incompressible(600<<10))
	fx.s.freeBytes = func(string) (uint64, error) { return 1<<20 + 50, nil }
	fx.s.oneFlight.mu.Lock()
	fx.s.oneFlight.total = 600 << 10
	fx.s.oneFlight.mu.Unlock()
	z := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, z.ID)
	if j.Status != "failed" {
		t.Fatalf("zip %s with 600 KiB of piece bytes in flight, want failed on the disk", j.Status)
	}
	got := decode[jobJSON](t, rawCall(t, fx.s, "GET", "/api/v1/jobs/"+z.ID, fx.key, "", nil))
	if got.ErrorCode != "storage_full" || got.ErrorReason != "disk" {
		t.Errorf("job error code %q reason %q, want storage_full with disk", got.ErrorCode, got.ErrorReason)
	}
}

// TestAccountShareCountsZipReservations: an upload's share of the disk counts the account's stored zips, bundles and
// imports as well as its uploads. A pending zip of 400 KiB, then an upload of 200 KiB, on a 1 MiB disk
// whose share is 512 KiB: the upload is refused with scope account.
func TestAccountShareCountsZipReservations(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) {
		c.Storage.MinFree = 50
		c.Upload.MaxDiskSharePercent = 50
	})
	fx.s.freeBytes = func(string) (uint64, error) { return 1<<20 + 50, nil }
	z := createZip(t, fx, nil, fx.a.ID)
	if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.SetZipSnapshot(context.Background(), z.Target, 400<<10, 1, 0, store.Now())
	}); err != nil {
		t.Fatal(err)
	}
	rec := createUpload(t, fx.s, fx.key, 200<<10, md("folder", fx.fld.ID, "name", "more.bin"))
	wantError(t, rec, http.StatusInsufficientStorage, "storage_full")
	if d := decodeErrorDetails(t, rec); d["scope"] != "account" {
		t.Errorf("scope = %v, want account", d["scope"])
	}
}
