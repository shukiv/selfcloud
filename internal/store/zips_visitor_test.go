// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// zipVisitorTestDB opens a store with one account, and returns it with a write helper and a fixed now.
func zipVisitorTestDB(t *testing.T) (*Store, func(func(tx *Tx) error), time.Time) {
	t.Helper()
	ctx := context.Background()
	s := openTest(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	write := func(fn func(tx *Tx) error) {
		t.Helper()
		if err := s.Write(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	write(func(tx *Tx) error {
		return tx.InsertAccount(ctx, &Account{ID: "acc_a", Name: "A", Status: AccountActive, CreatedAt: now, UpdatedAt: now})
	})
	return s, write, now
}

// TestZipVisitorCountsSeparateKinds: the signed-in caps count only non-visitor zips, and the visitor cap counts only
// the visitors' live zips (pending, or ready and not expired), so the two never spend each other's budget.
func TestZipVisitorCountsSeparateKinds(t *testing.T) {
	s, write, now := zipVisitorTestDB(t)
	ctx := context.Background()
	write(func(tx *Tx) error {
		if err := tx.InsertZip(ctx, &Zip{ID: "zip_member", AccountID: "acc_a", CreatedBy: "usr_a", CreatedKind: "user",
			CreatedAt: now, Roots: []string{"nod_a"}}); err != nil {
			return err
		}
		return tx.InsertZip(ctx, &Zip{ID: "zip_visitor", AccountID: "acc_a", CreatedBy: "lnk_a", CreatedKind: "share_link",
			CreatedAt: now, Roots: []string{"nod_a"}})
	})
	if n, err := s.CountPendingZips(ctx, "acc_a", ""); err != nil || n != 1 {
		t.Errorf("CountPendingZips = %d, %v; want 1, the member's only", n, err)
	}
	if n, err := s.CountPendingZips(ctx, "acc_a", "usr_a"); err != nil || n != 1 {
		t.Errorf("CountPendingZips of the member = %d, %v; want 1", n, err)
	}
	if n, err := s.CountAllPendingZips(ctx); err != nil || n != 1 {
		t.Errorf("CountAllPendingZips = %d, %v; want 1, the member's only", n, err)
	}
	if n, err := s.CountLiveVisitorZips(ctx, "acc_a", now); err != nil || n != 1 {
		t.Errorf("CountLiveVisitorZips = %d, %v; want 1, the visitor's pending zip", n, err)
	}
	// A ready visitor zip still holds its file until it expires, so it keeps its place in the count.
	write(func(tx *Tx) error {
		_, err := tx.CompleteZip(ctx, "zip_visitor", 10, strings.Repeat("a", 64), now.Add(time.Hour))
		return err
	})
	if n, err := s.CountLiveVisitorZips(ctx, "acc_a", now); err != nil || n != 1 {
		t.Errorf("CountLiveVisitorZips after ready = %d, %v; want 1, still unexpired", n, err)
	}
	// A member's ready zip never counts, even unexpired.
	write(func(tx *Tx) error {
		_, err := tx.CompleteZip(ctx, "zip_member", 10, strings.Repeat("b", 64), now.Add(time.Hour))
		return err
	})
	if n, err := s.CountLiveVisitorZips(ctx, "acc_a", now); err != nil || n != 1 {
		t.Errorf("CountLiveVisitorZips with a ready member zip = %d, %v; want 1, the visitor's only", n, err)
	}
	// An expired ready visitor zip leaves the count.
	if n, err := s.CountLiveVisitorZips(ctx, "acc_a", now.Add(2*time.Hour)); err != nil || n != 0 {
		t.Errorf("CountLiveVisitorZips after expiry = %d, %v; want 0", n, err)
	}
}

// TestZipVisitorLiveZipOfLink: a link's live zip is its pending or unexpired ready one; a failed or expired zip, and
// another link's zip, are not it.
func TestZipVisitorLiveZipOfLink(t *testing.T) {
	s, write, now := zipVisitorTestDB(t)
	ctx := context.Background()
	mk := func(id, link string) {
		write(func(tx *Tx) error {
			return tx.InsertZip(ctx, &Zip{ID: id, AccountID: "acc_a", CreatedBy: link, CreatedKind: "share_link",
				CreatedAt: now, Roots: []string{"nod_a"}})
		})
	}
	mk("zip_pending", "lnk_pending")
	mk("zip_ready", "lnk_ready")
	write(func(tx *Tx) error {
		_, err := tx.CompleteZip(ctx, "zip_ready", 10, strings.Repeat("a", 64), now.Add(time.Hour))
		return err
	})
	mk("zip_expired", "lnk_expired")
	write(func(tx *Tx) error {
		_, err := tx.CompleteZip(ctx, "zip_expired", 10, strings.Repeat("a", 64), now.Add(-time.Hour))
		return err
	})
	mk("zip_failed", "lnk_failed")
	write(func(tx *Tx) error { return tx.FailZip(ctx, "zip_failed", "", "", "no") })
	for _, c := range []struct{ link, want string }{
		{"lnk_pending", "zip_pending"},
		{"lnk_ready", "zip_ready"},
		{"lnk_expired", ""},
		{"lnk_failed", ""},
		{"lnk_none", ""},
	} {
		z, err := s.LiveZipOfLink(ctx, "acc_a", c.link, now)
		if c.want == "" {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: LiveZipOfLink = %v, %v; want ErrNotFound", c.link, z, err)
			}
			continue
		}
		if err != nil || z == nil || z.ID != c.want {
			t.Errorf("%s: LiveZipOfLink = %v, %v; want %s", c.link, z, err, c.want)
		}
	}
}

// TestZipVisitorZipIDsOfLink: every zip a link made is listed, in any state, so deleting the link removes them all.
func TestZipVisitorZipIDsOfLink(t *testing.T) {
	s, write, now := zipVisitorTestDB(t)
	ctx := context.Background()
	mk := func(id, link string) {
		write(func(tx *Tx) error {
			return tx.InsertZip(ctx, &Zip{ID: id, AccountID: "acc_a", CreatedBy: link, CreatedKind: "share_link",
				CreatedAt: now, Roots: []string{"nod_a"}})
		})
	}
	mk("zip_1", "lnk_a")
	mk("zip_2", "lnk_a")
	write(func(tx *Tx) error { return tx.FailZip(ctx, "zip_2", "", "", "no") })
	mk("zip_3", "lnk_b")
	ids, err := s.ZipIDsOfLink(ctx, "acc_a", "lnk_a")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "zip_1" || ids[1] != "zip_2" {
		t.Errorf("ZipIDsOfLink = %v, want [zip_1 zip_2]", ids)
	}
	if ids, err := s.ZipIDsOfLink(ctx, "acc_a", "lnk_none"); err != nil || len(ids) != 0 {
		t.Errorf("ZipIDsOfLink of a link with no zips = %v, %v; want none", ids, err)
	}
}

// TestZipJobByTarget: the job of a zip is found whatever its status, so a ready zip's succeeded job can be handed back
// on reuse, where UnfinishedJob (queued or running only) finds nothing.
func TestZipJobByTarget(t *testing.T) {
	s, write, now := zipVisitorTestDB(t)
	ctx := context.Background()
	write(func(tx *Tx) error {
		return tx.InsertJob(ctx, &Job{ID: "job_1", AccountID: "acc_a", Kind: "node.zip", Target: "zip_1",
			Status: JobSucceeded, CreatedAt: now, UpdatedAt: now})
	})
	j, err := s.JobByTarget(ctx, "acc_a", "node.zip", "zip_1")
	if err != nil || j == nil || j.ID != "job_1" {
		t.Errorf("JobByTarget = %v, %v; want job_1", j, err)
	}
	var unfinished *Job
	var uerr error
	write(func(tx *Tx) error {
		unfinished, uerr = tx.UnfinishedJob(ctx, "acc_a", "node.zip", "zip_1")
		return nil
	})
	if !errors.Is(uerr, ErrNotFound) {
		t.Errorf("UnfinishedJob of a succeeded job = %v, %v; want ErrNotFound", unfinished, uerr)
	}
	if _, err := s.JobByTarget(ctx, "acc_a", "node.zip", "zip_none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("JobByTarget of a missing target = %v; want ErrNotFound", err)
	}
}
