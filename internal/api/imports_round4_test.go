// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// stagingFoldersOf counts the staging folders of the account in the database.
func stagingFoldersOf(t *testing.T, fx *importFixture) int {
	t.Helper()
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM folders WHERE account_id = ? AND staged = 1`, fx.acc.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// stageAndYield starts an import of one file into parent (or the folder when parent is ""), and runs it until its
// staging has begun, so the archive is held and the import is still running. It returns the import.
func stageAndYield(t *testing.T, fx *importFixture, folder, parent string) importJSON {
	t.Helper()
	body := map[string]any{"source": "archive", "folder": folder, "upload": fx.archive(t, "one.zip",
		zipArchive(t, [][2]string{{"a.txt", "1"}}))}
	if parent != "" {
		body["parent"] = parent
	}
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
	wantStatus(t, rec, http.StatusAccepted)
	imp := decode[importJSON](t, rec)
	fx.s.importFault = func(stage string) error {
		if stage == "stage" {
			return jobs.ErrYield
		}
		return nil
	}
	if err := fx.runJob(t); !errors.Is(err, jobs.ErrYield) {
		t.Fatalf("the run did not yield at the stage: %v", err)
	}
	fx.s.importFault = nil
	return imp
}

// assertJobCodeOnly reads a job as the account's manager and checks that its error is the API code alone: no server
// path, no store text, and no cause of the failure (round 4, F8 and N6).
func assertJobCodeOnly(t *testing.T, fx *importFixture, jobID string) {
	t.Helper()
	rec := call(t, fx.s, "GET", "/api/v1/jobs/"+jobID, fx.key, nil)
	wantStatus(t, rec, http.StatusOK)
	got := decode[struct {
		Error string `json:"error"`
	}](t, rec)
	for _, leak := range []string{"/srv", "no such file", "SELECT", "store went away"} {
		if strings.Contains(got.Error, leak) {
			t.Fatalf("job %s names %q in its error %q", jobID, leak, got.Error)
		}
	}
	if got.Error != string(apierr.Internal) {
		t.Fatalf("job %s error = %q, want the code %q", jobID, got.Error, apierr.Internal)
	}
}

// leakyFaults makes the stage fail with a cause that names a server path, and the first cleanup fail with store text.
// The cleanup fails once, so a later run can finish it.
func leakyFaults(fx *importFixture) {
	cleanups := 0
	fx.s.importFault = func(stage string) error {
		switch stage {
		case "stage":
			return apierr.Wrap(apierr.Internal, "staging failed",
				errors.New("write /srv/selfcloud/blobs/blb_1: no such file or directory"))
		case "cleanup":
			cleanups++
			if cleanups == 1 {
				return errors.New("SELECT nodes: the store went away")
			}
		}
		return nil
	}
}

// A run that fails and whose cleanup dies reports the cleanup's error as the code only. The sweep queues the cleanup
// again, and the resumed run reports the code of the stored failure, not its cause's text (round 4, F8 and N6).
func TestImportResumeReportsOnlyTheCode(t *testing.T) {
	fx := newImportFixture(t)
	leakyFaults(fx)
	imp := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	if err := fx.runJob(t); err == nil {
		t.Fatal("the first run did not fail")
	}
	assertJobCodeOnly(t, fx, imp.Job)
	fx.s.importFault = nil
	n, err := fx.s.store.RequeueAbandonedImports(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("the sweep queued %d jobs (%v), want 1", n, err)
	}
	if err := fx.runJob(t); err == nil {
		t.Fatal("the resumed run did not report the failure")
	}
	assertJobCodeOnly(t, fx, fx.get(t, imp.ID).Job)
}

// Deleting the folder an import writes into ends the import in the same write: its archive's reservation is released
// once, and its staging folder goes with it (round 4, N1). Before, the row went by cascade and the reservation stayed.
func TestDeletingTheTargetEndsItsImport(t *testing.T) {
	fx := newImportFixture(t)
	target := newFolder(t, fx.s, fx.key, fx.acc.ID, "Target")
	root, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+root.ID+"/permissions/"+fx.user.ID, fx.key,
		map[string]any{"role": store.RoleEditor}), http.StatusOK)
	imp := stageAndYield(t, fx, target.ID, "")
	if held := reservedOf(t, fx); held == 0 {
		t.Fatal("the archive is not held before the delete")
	}
	if n := stagingFoldersOf(t, fx); n != 1 {
		t.Fatalf("%d staging folders before the delete, want 1", n)
	}
	rec := call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+target.ID, fx.key, nil)
	if rec.Code/100 != 2 {
		t.Fatalf("delete of the target = %d: %s", rec.Code, rec.Body.String())
	}
	if got := reservedOf(t, fx); got != 0 {
		t.Fatalf("reserved after the delete = %d, want 0", got)
	}
	if n := stagingFoldersOf(t, fx); n != 0 {
		t.Fatalf("%d staging folders left after the delete", n)
	}
	if _, err := fx.s.store.Import(context.Background(), imp.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the import is still there after its target was deleted: %v", err)
	}
}

// The same holds when the target directory is purged from the trash (round 4, N1).
func TestPurgingTheTargetDirectoryEndsItsImport(t *testing.T) {
	fx := newImportFixture(t)
	first := fx.startOK(t, fx.archive(t, "dir.zip", zipArchive(t, [][2]string{{"t/", ""}, {"t/b.txt", "2"}})), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("first import: %v", err)
	}
	_ = first
	dir := fx.children(t)["t"]
	imp := stageAndYield(t, fx, fx.folder, dir.ID)
	if held := reservedOf(t, fx); held == 0 {
		t.Fatal("the archive is not held before the purge")
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+dir.ID, fx.key, nil), http.StatusNoContent)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+dir.ID, fx.key, nil), http.StatusNoContent)
	if got := reservedOf(t, fx); got != 0 {
		t.Fatalf("reserved after the purge = %d, want 0", got)
	}
	if n := stagingFoldersOf(t, fx); n != 0 {
		t.Fatalf("%d staging folders left after the purge", n)
	}
	if _, err := fx.s.store.Import(context.Background(), imp.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the import is still there after its target was purged: %v", err)
	}
}

// A directory an earlier import made is reused by a later import of the same archive. Moved to another folder between
// the staging and the publish, it must not take the later import's files: the publish refuses it (round 4, N5).
func TestImportPublishRechecksReusedDirectories(t *testing.T) {
	fx := newImportFixture(t)
	// No entry for "t/" itself: the directory is made as the entry under it needs it, so a later import reuses it.
	arch := zipArchive(t, [][2]string{{"t/a.txt", "1"}})
	_ = fx.startOK(t, fx.archive(t, "dir.zip", arch), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("first import: %v", err)
	}
	dir := fx.children(t)["t"]
	// The file the first import wrote is removed, so the second import writes it again, under the reused directory.
	rec := callSession(t, fx.s, fx.sess, "GET", "/api/v1/nodes/"+dir.ID+"/children", nil)
	wantStatus(t, rec, http.StatusOK)
	kids := decode[listResponse[nodeJSON]](t, rec).Items
	if len(kids) != 1 {
		t.Fatalf("the directory holds %d items, want 1", len(kids))
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+kids[0].ID, fx.key, nil), http.StatusNoContent)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+kids[0].ID, fx.key, nil), http.StatusNoContent)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	yielded := false
	fx.s.importFault = func(stage string) error {
		if stage == "publish" && !yielded {
			yielded = true
			return jobs.ErrYield
		}
		return nil
	}
	imp := fx.startOK(t, fx.archive(t, "dir.zip", arch), "")
	_ = fx.runJob(t) // yields at publish
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+dir.ID+"/move", fx.key,
		map[string]any{"folder": other.ID}), http.StatusOK)
	_ = fx.runJob(t)
	fx.s.importFault = nil
	got := fx.get(t, imp.ID)
	if got.State != store.ImportFailed || got.ErrorCode != string(apierr.Conflict) {
		t.Fatalf("after the move: state %s code %q, want failed with conflict", got.State, got.ErrorCode)
	}
}

// A scan counts the declared size of the user's other unfinished imports: one paused import holds 600 of a 1000 quota
// before it is staged, so a second import of 600 is refused at its scan, not left to fill the disk (round 4, F6).
func TestImportScanCountsTheOtherUnfinishedImports(t *testing.T) {
	fx := newImportFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 1000}), http.StatusOK)
	fx.s.importFault = func(stage string) error {
		if stage == "stage" {
			return jobs.ErrYield
		}
		return nil
	}
	a := fx.startOK(t, fx.archive(t, "a.zip", zipArchive(t, [][2]string{{"a.bin", strings.Repeat("0", 600)}})), "")
	if err := fx.runJob(t); !errors.Is(err, jobs.ErrYield) {
		t.Fatalf("the first import did not scan and yield at the stage: %v", err)
	}
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+a.ID+"/pause", nil), http.StatusOK)
	_ = fx.runJob(t) // the paused import's job ends
	b := fx.startOK(t, fx.archive(t, "b.zip", zipArchive(t, [][2]string{{"b.bin", strings.Repeat("0", 600)}})), "")
	_ = fx.runJob(t)
	fx.s.importFault = nil
	got := fx.get(t, b.ID)
	if got.State != store.ImportFailed || got.ErrorCode != string(apierr.QuotaExceeded) {
		t.Fatalf("the second import: state %s code %q, want failed with quota_exceeded", got.State, got.ErrorCode)
	}
}

// An import whose cleanup fails every time is requeued by the sweep a few times, then not again: it is failed with a code,
// so the sweep does not queue it for ever (round 4, N7).
func TestPoisonImportIsNotRequeuedForever(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importFault = func(stage string) error {
		switch stage {
		case "stage":
			return errors.New("the stage broke")
		case "cleanup":
			return errors.New("the cleanup broke")
		}
		return nil
	}
	imp := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	_ = fx.runJob(t)
	requeued := 0
	for i := 0; i < 6; i++ {
		n, err := fx.s.store.RequeueAbandonedImports(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		requeued++
		_ = fx.runJob(t)
	}
	fx.s.importFault = nil
	if requeued > 2 {
		t.Fatalf("the sweep requeued the import %d times; it should stop after the third failed job", requeued)
	}
	if got := fx.get(t, imp.ID); got.ErrorCode != string(apierr.Internal) {
		t.Fatalf("the poison import's error code = %q, want %q", got.ErrorCode, apierr.Internal)
	}
}

// The unclaimed-archive window runs from the last write, not from the creation: an archive written for longer than a day
// is not cut off while it is being written (round 4, N3).
func TestImportArchiveExpiryRunsFromTheLastWrite(t *testing.T) {
	fx := newImportFixture(t, func(c *config.Config) { c.Upload.Expiry = config.Duration(72 * time.Hour) })
	now := time.Now()
	up := &store.Upload{CreatedAt: now.Add(-30 * time.Hour), UpdatedAt: now, Purpose: store.UploadPurposeImport}
	want := now.Add(24 * time.Hour)
	if got := fx.s.uploadDeadlineFor(up, now); !got.Equal(want) {
		t.Fatalf("a write now expires the archive at %v, want %v (a day from the write)", got, want)
	}
}

// A restart runs the same job row again. The resumed run reports the code only, as the sweep's does (round 4, F8).
func TestImportRestartReportsOnlyTheCode(t *testing.T) {
	fx := newImportFixture(t)
	leakyFaults(fx)
	imp := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	ctx := context.Background()
	job, err := fx.s.store.ClaimJobOf(ctx, jobImport)
	if err != nil {
		t.Fatalf("no import job is queued: %v", err)
	}
	// The process dies after the failure is recorded: the job row stays running, and nothing finishes it.
	if err := fx.s.runImport(ctx, job); err == nil {
		t.Fatal("the first run did not fail")
	}
	fx.s.importFault = nil
	if n, err := fx.s.store.RequeueRunningJobs(ctx); err != nil || n != 1 {
		t.Fatalf("restart requeued %d jobs (%v), want 1", n, err)
	}
	again, err := fx.s.store.ClaimJobOf(ctx, jobImport)
	if err != nil {
		t.Fatalf("the restarted job is not queued: %v", err)
	}
	if again.ID != job.ID {
		t.Fatalf("the restart made job %s, want the same row %s", again.ID, job.ID)
	}
	runErr := fx.s.runImport(ctx, again)
	if err := fx.s.store.FinishJob(ctx, again.ID, runErr); err != nil {
		t.Fatal(err)
	}
	if runErr == nil {
		t.Fatal("the resumed run did not report the failure")
	}
	assertJobCodeOnly(t, fx, imp.Job)
}
