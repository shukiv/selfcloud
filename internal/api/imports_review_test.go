// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// A reservation a stopped run left is released before the entry is reserved again, so the entry is held once.
func TestImportReservationIsHeldOnce(t *testing.T) {
	fx := newImportFixture(t)
	held := false
	fx.s.importFault = func(stage string) error {
		if stage == "reserved" && !held {
			held = true
			return jobs.ErrYield
		}
		return nil
	}
	data := zipArchive(t, [][2]string{{"a.txt", "1"}})
	upl := fx.archive(t, "one.zip", data)
	imp := fx.startOK(t, upl, "")
	if err := fx.runJob(t); !errors.Is(err, jobs.ErrYield) {
		t.Fatalf("first run = %v, want a yield", err)
	}
	// The archive is held by the staging folder, and the entry's one byte is held beside it.
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != int64(len(data))+1 {
		t.Fatalf("reserved after the yield = %d, want the archive's %d and the entry's 1 byte", u.ReservedBytes, len(data))
	}
	if err := fx.runJob(t); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportDone || got.Files != 1 {
		t.Fatalf("after resume: %+v, want done with one file", got)
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 0 {
		t.Fatalf("reserved after the import = %d, want 0", u.ReservedBytes)
	}
}

// An account.delete never runs beside another job of its account, and no import job starts while it runs.
func TestAccountDeleteDoesNotOverlapImports(t *testing.T) {
	fx := newImportFixture(t)
	ctx := context.Background()
	upl := fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}}))
	fx.startOK(t, upl, "")
	running, err := fx.s.store.ClaimJobOf(ctx, jobImport)
	if err != nil {
		t.Fatal(err)
	}
	queueDelete(t, fx, ctx)
	if _, err := fx.s.store.ClaimJobOf(ctx, jobAccountDelete); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("account.delete claimed beside a running import: err = %v, want ErrNotFound", err)
	}
	// The import yields its turn: it is queued again, and the deletion may run.
	if err := fx.s.store.YieldJob(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	del, err := fx.s.store.ClaimJobOf(ctx, jobAccountDelete)
	if err != nil {
		t.Fatalf("account.delete not claimed with no import running: %v", err)
	}
	if _, err := fx.s.store.ClaimJobOf(ctx, jobImport); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("import claimed while account.delete runs: err = %v, want ErrNotFound", err)
	}
	if err := fx.s.store.FinishJob(ctx, del.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.s.store.ClaimJobOf(ctx, jobImport); err != nil {
		t.Fatalf("import not claimed once the deletion ends: %v", err)
	}
}

func queueDelete(t *testing.T, fx *importFixture, ctx context.Context) {
	t.Helper()
	now := store.Now()
	err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertJob(ctx, &store.Job{ID: id.New("job"), AccountID: fx.acc.ID, Kind: jobAccountDelete,
			Target: fx.acc.ID, Status: store.JobQueued, CreatedAt: now, UpdatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An entry is matched to an earlier import only for the same user, the same archive into the same folder and parent.
func TestImportDedupeIsScopedToUserAndPlace(t *testing.T) {
	fx := newImportFixture(t)
	data := zipArchive(t, [][2]string{{"d/", ""}, {"d/a.txt", "1"}, {"b.txt", "2"}})
	first := fx.startOK(t, fx.archive(t, "tree.zip", data), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if got := fx.get(t, first.ID); got.Files != 2 {
		t.Fatalf("first import Files = %d, want 2", got.Files)
	}

	// The same archive into the same folder and parent is the same import: nothing is copied twice.
	again := fx.startOK(t, fx.archive(t, "tree-again.zip", data), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("same place: %v", err)
	}
	if got := fx.get(t, again.ID); got.Files != 0 || got.Skipped != 3 {
		t.Fatalf("same place = %+v, want 3 skipped and no files", got)
	}

	// The same archive into a directory of the folder is a different place: its entries are copied again.
	d := fx.children(t)["d"]
	body := map[string]any{"source": "archive", "upload": fx.archive(t, "tree-in-d.zip", data), "folder": fx.folder,
		"parent": d.ID}
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
	wantStatus(t, rec, 202)
	inD := decode[importJSON](t, rec)
	if err := fx.runJob(t); err != nil {
		t.Fatalf("other parent: %v", err)
	}
	if got := fx.get(t, inD.ID); got.Files != 2 || got.Skipped != 0 {
		t.Fatalf("other parent = %+v, want 2 files copied again", got)
	}

}

// Another user with the same archive does not see the first user's copy: the match is not account-wide, and nothing of
// another user's import is disclosed by a skip.
func TestImportDedupeDoesNotCrossUsers(t *testing.T) {
	fx := newImportFixture(t)
	data := zipArchive(t, [][2]string{{"d/", ""}, {"d/a.txt", "1"}, {"b.txt", "2"}})
	fx.startOK(t, fx.archive(t, "tree.zip", data), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("first user: %v", err)
	}
	bob, bobSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "bob@example.com")
	bobPF, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	bobFx := &importFixture{s: fx.s, admin: fx.admin, key: fx.key, acc: fx.acc, user: bob, sess: bobSess,
		folder: bobPF.ID}
	other := bobFx.startOK(t, bobFx.archive(t, "tree.zip", data), "")
	if err := bobFx.runJob(t); err != nil {
		t.Fatalf("other user: %v", err)
	}
	if got := bobFx.get(t, other.ID); got.Files != 2 || got.Skipped != 0 {
		t.Fatalf("other user = %+v, want 2 files copied, none taken from another user", got)
	}
}

// Two archives with the same paths are two imports: the second copies its own files.
func TestImportTwoArchivesWithTheSamePathsAreBothImported(t *testing.T) {
	fx := newImportFixture(t)
	fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	if err := fx.runJob(t); err != nil {
		t.Fatal(err)
	}
	second := fx.startOK(t, fx.archive(t, "two.zip", zipArchive(t, [][2]string{{"a.txt", "2"}})), "")
	if err := fx.runJob(t); err != nil {
		t.Fatal(err)
	}
	if got := fx.get(t, second.ID); got.Files != 1 || got.Skipped != 0 {
		t.Fatalf("second archive = %+v, want its one file copied", got)
	}
}

// An archive holds disk, not quota: creating an import releases no reservation it never took, so another upload's
// reservation in the same folder is left as it was.
func TestImportArchiveReleasesNoOtherReservation(t *testing.T) {
	fx := newImportFixture(t)
	other := upSession(t, fx.s, fx.sess, "POST", "/api/v1/uploads", map[string]string{
		"Upload-Length": "5", "Upload-Metadata": md("folder", fx.folder, "name", "other.txt")}, nil, 0)
	wantStatus(t, other, http.StatusCreated)
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 5 {
		t.Fatalf("reserved by the other upload = %d, want 5", u.ReservedBytes)
	}
	data := zipArchive(t, [][2]string{{"a.txt", "1"}})
	fx.startOK(t, fx.archive(t, "one.zip", data), "")
	// The archive's length moved to the staging folder, which holds it until the import ends.
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.ReservedBytes != 5+int64(len(data)) {
		t.Fatalf("reserved after the import is created = %d, want the other upload's 5 and the archive's %d", u.ReservedBytes, len(data))
	}
}

// An import archive is never committed as a file: the commit path refuses it, and no node is made.
func TestImportArchiveCannotBeCommittedAsAFile(t *testing.T) {
	fx := newImportFixture(t)
	upl := fx.archive(t, "stolen.zip", zipArchive(t, [][2]string{{"a.txt", "1"}}))
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/uploads/"+upl+"/commit", map[string]any{"name": "stolen.zip"})
	if rec.Code == http.StatusCreated {
		t.Fatalf("commit of an import archive = 201, want it refused")
	}
	if _, ok := fx.children(t)["stolen.zip"]; ok {
		t.Fatalf("an import archive became a file in the folder")
	}
	if u := usageOf(t, fx.s, fx.key, fx.acc.ID); u.Files != 0 {
		t.Fatalf("files after a refused commit = %d, want 0", u.Files)
	}
}

// A user may hold a limited number of unfinished imports, paused ones included: a paused import keeps its archive.
func TestImportCapsUnfinishedImportsPerUser(t *testing.T) {
	fx := newImportFixture(t, func(c *config.Config) { c.Imports.MaxUnfinishedPerUser = 2 })
	var ids []string
	for _, name := range []string{"one", "two"} {
		imp := fx.startOK(t, fx.archive(t, name+".zip", zipArchive(t, [][2]string{{name + ".txt", "1"}})), "")
		wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/pause", nil), http.StatusOK)
		ids = append(ids, imp.ID)
	}
	rec := fx.start(t, fx.archive(t, "three.zip", zipArchive(t, [][2]string{{"three.txt", "1"}})), "")
	if rec.Code == http.StatusAccepted || !strings.Contains(rec.Body.String(), "too_many_unfinished") {
		t.Fatalf("third unfinished import = %d %s, want too_many_unfinished", rec.Code, rec.Body.String())
	}
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+ids[0]+"/cancel", nil), http.StatusOK)
	fx.startOK(t, fx.archive(t, "four.zip", zipArchive(t, [][2]string{{"four.txt", "1"}})), "")
}

// A paused import is cancelled once it has been paused past the expiry, and its cleanup job runs.
func TestPausedImportExpires(t *testing.T) {
	fx := newImportFixture(t)
	ctx := context.Background()
	imp := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/pause", nil), http.StatusOK)

	if n, err := fx.s.store.ExpirePausedImports(ctx, time.Now().Add(-time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("paused just now: expired %d (%v), want none before the expiry", n, err)
	}
	if n, err := fx.s.store.ExpirePausedImports(ctx, time.Now().Add(time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("paused past the expiry: expired %d (%v), want 1", n, err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportCancelled {
		t.Fatalf("after expiry state = %s, want cancelled", got.State)
	}
	if err := fx.runJob(t); err != nil {
		t.Fatalf("the expiry's cleanup job: %v", err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportCancelled {
		t.Fatalf("after cleanup state = %s, want cancelled", got.State)
	}
}

// teamImport is an import fixture whose user, ann, is an editor of a team space: the rights an import into the space needs.
func teamImport(t *testing.T) (*importFixture, *sharingFixture) {
	t.Helper()
	sf := newSharingFixture(t)
	sf.setPerm(t, sf.rootOf(t, sf.fld.ID), sf.ann.ID, store.RoleEditor)
	fx := &importFixture{s: sf.s, admin: sf.admin, key: sf.key, acc: sf.acc, user: sf.ann, sess: sf.annSess,
		folder: sf.fld.ID}
	return fx, sf
}

// The rights to write to the target are checked at publish: a user demoted before the import publishes fails it, and
// nothing reaches the team space.
func TestImportRightsAreCheckedAtPublish(t *testing.T) {
	fx, sf := teamImport(t)
	imp := fx.startOK(t, fx.archive(t, "team.zip", zipArchive(t, [][2]string{{"x.txt", "1"}})), "")
	sf.setPerm(t, sf.rootOf(t, sf.fld.ID), sf.ann.ID, store.RoleViewer)
	_ = fx.runJob(t)
	got := fx.get(t, imp.ID)
	if got.State != store.ImportFailed || got.ErrorCode != string(apierr.Forbidden) {
		t.Fatalf("after the demotion: %+v, want failed with forbidden", got)
	}
	if _, ok := fx.children(t)["x.txt"]; ok {
		t.Fatal("a file reached the team space after its user lost the right to write there")
	}
}

// The rights are checked again at resume: a paused import whose user was demoted is refused, and its staged tree goes.
func TestImportRightsAreCheckedAtResume(t *testing.T) {
	fx, sf := teamImport(t)
	imp := fx.startOK(t, fx.archive(t, "team.zip", zipArchive(t, [][2]string{{"x.txt", "1"}})), "")
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/pause", nil), http.StatusOK)
	sf.setPerm(t, sf.rootOf(t, sf.fld.ID), sf.ann.ID, store.RoleViewer)
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/resume", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("resume after the demotion = %d %s, want 403", rec.Code, rec.Body.String())
	}
	got := fx.get(t, imp.ID)
	if got.State != store.ImportFailed || got.ErrorCode != string(apierr.Forbidden) {
		t.Fatalf("after the refused resume: %+v, want failed with forbidden", got)
	}
	_ = fx.runJob(t)
	if _, ok := fx.children(t)["x.txt"]; ok {
		t.Fatal("a file reached the team space after its user lost the right to write there")
	}
}
