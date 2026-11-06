// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// archiveUpload starts an upload of an import archive of length bytes, none of them sent, and returns the answer.
func (fx *importFixture) archiveUpload(t *testing.T, length int64, name string) *httptest.ResponseRecorder {
	t.Helper()
	return upSession(t, fx.s, fx.sess, "POST", "/api/v1/uploads", map[string]string{
		"Upload-Length": strconv.FormatInt(length, 10), "Upload-Metadata": md("kind", "import", "name", name)}, nil, 0)
}

// reservedOf is the account's reserved bytes, as the usage route reports them.
func reservedOf(t *testing.T, fx *importFixture) int64 {
	t.Helper()
	return usageOf(t, fx.s, fx.key, fx.acc.ID).ReservedBytes
}

// A member at quota cannot start an import upload of 2 GiB: the archive reserves its length like any upload (F1).
func TestImportArchiveIsReservedAgainstQuota(t *testing.T) {
	fx := newImportFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 1 << 20}), http.StatusOK)
	rec := fx.archiveUpload(t, 2<<30, "big.zip")
	if rec.Code == http.StatusCreated || !strings.Contains(rec.Body.String(), "quota_exceeded") {
		t.Fatalf("import upload of 2 GiB at a 1 MiB quota = %d %s, want quota_exceeded", rec.Code, rec.Body.String())
	}
}

// The archive's reservation is held from its upload until its import ends, and released once however the import ends
// (done, cancelled, failed, expired). Another upload's reservation in the folder must survive it, so a second release
// would show as 0 instead of the other upload's bytes (F1, F3).
func TestImportArchiveReservationEndsOnce(t *testing.T) {
	for _, end := range []string{"done", "cancelled", "failed", "expired"} {
		t.Run(end, func(t *testing.T) {
			fx := newImportFixture(t)
			wantStatus(t, fx.archiveUpload(t, 5, "other.txt"), http.StatusCreated)
			data := zipArchive(t, [][2]string{{"a.txt", "1234567890"}})
			imp := fx.startOK(t, fx.archive(t, "one.zip", data), "")
			if got, want := reservedOf(t, fx), int64(len(data))+5; got != want {
				t.Fatalf("reserved while the archive is held = %d, want %d (the archive and the other upload)", got, want)
			}
			switch end {
			case "done":
				if err := fx.runJob(t); err != nil {
					t.Fatalf("import: %v", err)
				}
			case "cancelled":
				wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/cancel", nil), http.StatusOK)
				_ = fx.runJob(t)
			case "failed":
				fx.s.importFault = func(stage string) error {
					if stage == "stage" {
						return errors.New("the stage failed")
					}
					return nil
				}
				_ = fx.runJob(t)
				fx.s.importFault = nil
			case "expired":
				wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/pause", nil), http.StatusOK)
				if n, err := fx.s.store.ExpirePausedImports(context.Background(), time.Now().Add(time.Hour), 10); err != nil || n != 1 {
					t.Fatalf("expire: %d, %v", n, err)
				}
				_ = fx.runJob(t)
			}
			if got := reservedOf(t, fx); got != 5 {
				t.Fatalf("reserved after the import ended (%s) = %d, want the other upload's 5 only", end, got)
			}
		})
	}
}

// An account holds a limited number of import uploads that no import has claimed (default 3) (F1).
func TestImportArchiveUploadsAreCapped(t *testing.T) {
	fx := newImportFixture(t)
	for i := 0; i < 3; i++ {
		wantStatus(t, fx.archiveUpload(t, 4, fmt.Sprintf("a%d.zip", i)), http.StatusCreated)
	}
	rec := fx.archiveUpload(t, 4, "a3.zip")
	if rec.Code == http.StatusCreated || !strings.Contains(rec.Body.String(), "too_many_archives") {
		t.Fatalf("fourth unclaimed archive = %d %s, want too_many_archives", rec.Code, rec.Body.String())
	}
}

// An import upload that no import claimed expires after a day (imports.unclaimed_expiry_hours), even when uploads
// themselves live longer. Its reservation goes with it (F1, F3).
func TestImportArchiveUploadExpiresAfterADay(t *testing.T) {
	fx := newImportFixture(t, func(c *config.Config) { c.Upload.Expiry = config.Duration(72 * time.Hour) })
	upl := fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}}))
	expire := func(at time.Time) bool {
		var gone bool
		err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
			var err error
			gone, err = tx.DeleteExpiredUpload(context.Background(), fx.acc.ID, upl, at, time.Time{})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return gone
	}
	if expire(time.Now().Add(23 * time.Hour)) {
		t.Fatal("an unclaimed archive expired before a day")
	}
	if !expire(time.Now().Add(25 * time.Hour)) {
		t.Fatal("an unclaimed archive did not expire after a day")
	}
	if got := reservedOf(t, fx); got != 0 {
		t.Fatalf("reserved after the archive expired = %d, want 0", got)
	}
}

// A recount, run while an import holds its archive, keeps the archive's reservation on the staging folder; the two
// totals agree (F3).
func TestRecountKeepsTheArchiveReservation(t *testing.T) {
	fx := newImportFixture(t)
	data := zipArchive(t, [][2]string{{"a.txt", "1"}})
	fx.startOK(t, fx.archive(t, "one.zip", data), "")
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		_, _, err := tx.RecountAccount(ctx, fx.acc.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := reservedOf(t, fx); got != int64(len(data)) {
		t.Fatalf("reserved after a recount = %d, want the archive's %d", got, len(data))
	}
	var counted, declared int64
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		var err error
		counted, declared, err = tx.ReservationTotals(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if counted != declared {
		t.Fatalf("reservation totals disagree after a recount: counted %d, declared %d", counted, declared)
	}
}

// A drop-link visitor cannot send an import archive, and nothing is reserved for the attempt (F2).
func TestDropVisitorCannotUploadAnArchive(t *testing.T) {
	fx := newDropFixture(t)
	_, secret := fx.makeDrop(t, fx.b.ID, nil)
	tok := fx.visitDrop(t, secret, "")
	before := fx.reservedBytes(t)
	rec := createUpload(t, fx.s, tok, 1024, md("name", "x.zip", "kind", "import"))
	if rec.Code == http.StatusCreated || !strings.Contains(rec.Body.String(), "forbidden") {
		t.Fatalf("visitor's import upload = %d %s, want forbidden", rec.Code, rec.Body.String())
	}
	if got := fx.reservedBytes(t); got != before {
		t.Fatalf("reserved after the refused upload = %d, want %d", got, before)
	}
}

// The disk check before an entry counts the entry: an entry larger than the usable space pauses the import with
// storage_full, and no byte of it is written (F5).
func TestImportEntryLargerThanTheDiskPauses(t *testing.T) {
	fx := newImportFixture(t)
	minFree := uint64(fx.s.cfg.Storage.MinFree)
	fx.s.freeBytes = func(string) (uint64, error) { return minFree + 1024, nil }
	imp := fx.startOK(t, fx.archive(t, "big.zip", zipArchive(t, [][2]string{{"big.txt", strings.Repeat("0", 10240)}})), "")
	_ = fx.runJob(t)
	got := fx.get(t, imp.ID)
	if got.State != store.ImportPaused || got.PauseReason != string(apierr.StorageFull) || got.Files != 0 {
		t.Fatalf("entry of 10 KiB on 1 KiB usable: %+v, want paused with storage_full and no file", got)
	}
}

// A publish that writes one audit row per file floods the webhooks; the folder is reported once, as a copy reports a
// large tree (F4).
func TestImportPublishDoesNotFloodWebhooks(t *testing.T) {
	wf := newWebhookFixture(t)
	wf.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
	u, sess := verifiedUser(t, wf.s, wf.key, wf.acc.ID, "ann@example.com")
	pf, err := wf.s.store.PersonalFolder(context.Background(), wf.acc.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx := &importFixture{s: wf.s, key: wf.key, acc: wf.acc, user: u, sess: sess, folder: pf.ID}
	var entries [][2]string
	for i := 0; i < 1100; i++ {
		entries = append(entries, [2]string{fmt.Sprintf("f%04d.txt", i), "x"})
	}
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	fx.startOK(t, fx.archive(t, "many.zip", zipArchive(t, entries)), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("import: %v", err)
	}
	files := 0
	for _, ev := range outboxOf(t, fx.s, fx.acc.ID)[seen:] {
		if ev.Type == "file.created" {
			files++
		}
	}
	if files > 1 {
		t.Fatalf("%d file.created events for 1100 files; want one folder summary", files)
	}
}

// An import whose job ended without finishing its cleanup gets a job again from the store's requeue (F10).
func TestAbandonedImportIsQueuedAgain(t *testing.T) {
	fx := newImportFixture(t)
	imp := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/cancel", nil), http.StatusOK)
	ctx := context.Background()
	job, err := fx.s.store.ClaimJobOf(ctx, jobImport)
	if err != nil {
		t.Fatal(err)
	}
	// The job ends without its cleanup: the import is cancelled and has no job.
	if err := fx.s.store.FinishJob(ctx, job.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.store.ClaimJobOf(ctx, jobImport); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a job is queued for the lost import: %v", err)
	}
	if n, err := fx.s.requeueImports(ctx); err != nil || n != 1 {
		t.Fatalf("requeue: %d, %v; want one import queued again", n, err)
	}
	if err := fx.runJob(t); err != nil {
		t.Fatalf("the requeued cleanup: %v", err)
	}
	if n, err := fx.s.requeueImports(ctx); err != nil || n != 0 {
		t.Fatalf("after the cleanup: %d, %v; want nothing to queue", n, err)
	}
}

// The archive's digest is read or the import is refused: an empty hash would make different archives dedupe against
// each other (F15).
func TestArchiveDigestErrorIsRefused(t *testing.T) {
	if _, err := importDigest(&store.Upload{HashState: []byte("not a hash state"), Length: 3}); err == nil {
		t.Fatal("a digest that cannot be read was accepted")
	}
}

// A job's error names no server path and no SQL text: the user sees the code (F8).
func TestImportFailureLeaksNoServerText(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importFault = func(stage string) error {
		if stage == "stage" {
			return errors.New("write /srv/selfcloud/blobs/blb_1: no such file or directory")
		}
		return nil
	}
	imp := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	_ = fx.runJob(t)
	fx.s.importFault = nil
	if got := fx.get(t, imp.ID); got.State != store.ImportFailed {
		t.Fatalf("state = %s, want failed", got.State)
	}
	rec := callSession(t, fx.s, fx.sess, "GET", "/api/v1/jobs/"+imp.Job, nil)
	wantStatus(t, rec, http.StatusOK)
	for _, leak := range []string{"/srv/selfcloud", "no such file"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("the job names %q: %s", leak, rec.Body.String())
		}
	}
}

// A cleanup that has not ended keeps its import's record: retention does not purge it (F9).
func TestRetentionKeepsAnImportWhoseCleanupDidNotEnd(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importFault = func(stage string) error {
		switch stage {
		case "stage":
			return errors.New("the stage failed")
		case "cleanup":
			return errors.New("the store went away")
		}
		return nil
	}
	fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	_ = fx.runJob(t)
	fx.s.importFault = nil
	n, err := fx.s.store.PurgeImports(context.Background(), time.Now().Add(time.Hour), 100)
	if err != nil || n != 0 {
		t.Fatalf("purge removed %d records of an import whose cleanup did not end (%v)", n, err)
	}
}

// The target directory is checked again at publish: moved to another folder between staging and publish, the import
// fails and writes nothing into the moved directory (F17).
func TestImportTargetMovedBeforePublishFails(t *testing.T) {
	fx := newImportFixture(t)
	first := fx.startOK(t, fx.archive(t, "dir.zip", zipArchive(t, [][2]string{{"t/", ""}, {"t/a.txt", "1"}})), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("first import: %v", err)
	}
	_ = first
	dir := fx.children(t)["t"]
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	// The user may write in both folders, so the role check alone does not refuse the moved directory.
	otherRoot, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+otherRoot.ID+"/permissions/"+fx.user.ID, fx.key,
		map[string]any{"role": store.RoleEditor}), http.StatusOK)
	yielded := false
	fx.s.importFault = func(stage string) error {
		if stage == "publish" && !yielded {
			yielded = true
			return jobs.ErrYield
		}
		return nil
	}
	body := map[string]any{"source": "archive", "folder": fx.folder, "parent": dir.ID,
		"upload": fx.archive(t, "b.zip", zipArchive(t, [][2]string{{"b.txt", "2"}}))}
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
	wantStatus(t, rec, http.StatusAccepted)
	imp := decode[importJSON](t, rec)
	_ = fx.runJob(t) // yields at publish
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+dir.ID+"/move", fx.key,
		map[string]any{"folder": other.ID}), http.StatusOK)
	_ = fx.runJob(t)
	fx.s.importFault = nil
	got := fx.get(t, imp.ID)
	if got.State != store.ImportFailed || got.ErrorCode != string(apierr.Conflict) {
		t.Fatalf("after the move: %+v, want failed with conflict", got)
	}
}
