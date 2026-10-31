// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestCopyOnConflictIsValidatedOnBothPaths: an on_conflict value other than fail, rename or replace is invalid_request
// for a small copy and for a chunked one, whether or not a name clashes. Nothing is queued or copied.
func TestCopyOnConflictIsValidatedOnBothPaths(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	file := putNode(t, fx.s, fx.key, fx.f.ID, "/one.txt", "x")
	for _, v := range []string{"bogus", "REPLACE"} {
		before := usageOf(t, fx.s, fx.key, fx.acc.ID)
		// The names are new, so no name clashes: the value is checked with no clash to find it, as the bug was.
		small := call(t, fx.s, "POST", "/api/v1/nodes/"+file.ID+"/copy", fx.key,
			map[string]any{"on_conflict": v, "name": "fresh-" + v + ".txt"})
		wantError(t, small, 400, apierr.InvalidRequest)
		chunked := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", fx.key,
			map[string]any{"on_conflict": v, "name": "fresh-" + v})
		wantError(t, chunked, 400, apierr.InvalidRequest)
		if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != before.ReservedBytes {
			t.Errorf("reserved = %d after a refused copy, was %d", u.ReservedBytes, before.ReservedBytes)
		}
	}
}

// TestReservationCountsOnlyWhatTheCopyMakes: a trashed file is not copied, so the reservation leaves it out. The tree
// has five one-byte files; with one trashed the copy reserves four bytes, not five.
func TestReservationCountsOnlyWhatTheCopyMakes(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	rawExec(t, fx.s, `UPDATE nodes SET trashed_at = 1 WHERE name = 'a.txt' AND folder_id = ?`, fx.f.ID)
	fx.queue(t)
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 4 {
		t.Errorf("reserved = %d, want 4 (the trashed file is not copied)", u.ReservedBytes)
	}
}

// TestCopyRefusedWhenTheDestinationUserIsFull: a chunked copy into a member's personal folder is refused at the request
// when its bytes would pass that member's limit, as a small copy is, and nothing is reserved.
func TestCopyRefusedWhenTheDestinationUserIsFull(t *testing.T) {
	t.Parallel()
	fx := newP1Fixture(t)
	fx.s.copySyncNodes = 3
	src := newFolder(t, fx.s, fx.key, fx.acc.ID, "Src")
	tree := bigTree(t, fx.s, fx.key, src.ID) // five one-byte files
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"quota_bytes": 3}), 200)
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+tree.ID+"/copy", fx.key,
		map[string]any{"folder": fx.annPF, "name": "copy"})
	wantError(t, rec, 507, apierr.QuotaExceeded)
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != before.ReservedBytes {
		t.Errorf("reserved = %d after the refusal, was %d", u.ReservedBytes, before.ReservedBytes)
	}
}

// TestChunkedJobWithoutItsStateFailsAndNeverFallsBack: a chunked job whose copy row is gone fails with nothing copied.
// It does not run as the one-write copy, which would copy the whole tree in one write.
func TestChunkedJobWithoutItsStateFailsAndNeverFallsBack(t *testing.T) {
	t.Parallel()
	fx := newChunkedFixture(t)
	fx.queue(t)
	rawExec(t, fx.s, `DELETE FROM copy_jobs WHERE job_id = ?`, fx.job.ID)
	done := fx.run(t)
	if done.Status != store.JobFailed {
		t.Fatalf("job = %+v, want failed", done)
	}
	if !strings.Contains(done.Error, "copy_state_lost") {
		t.Errorf("job error = %q, want copy_state_lost", done.Error)
	}
	if n := fx.topLevel(t); n != 1 {
		t.Errorf("top-level entries = %d, want the original 1: the job copied something", n)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.job.Target, fx.key, nil), 404, apierr.NotFound)
}
