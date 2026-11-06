// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
	"github.com/shukiv/selfcloud/internal/webhook"
)

// importFixture is an account with a verified user, who signs in, and whose personal folder is the target of imports.
type importFixture struct {
	s      *Server
	admin  string
	key    string
	acc    accountJSON
	user   userResponse
	sess   testSession
	folder string
}

func newImportFixture(t *testing.T, mutate ...func(*config.Config)) *importFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate...)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	u, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	pf, err := s.store.PersonalFolder(context.Background(), acc.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &importFixture{s: s, admin: admin, key: key, acc: acc, user: u, sess: sess, folder: pf.ID}
}

// archive uploads data as an import archive of the user, and returns the upload's ID.
func (fx *importFixture) archive(t *testing.T, name string, data []byte) string {
	t.Helper()
	rec := upSession(t, fx.s, fx.sess, "POST", "/api/v1/uploads", map[string]string{
		"Upload-Length": strconv.Itoa(len(data)), "Upload-Metadata": md("kind", "import", "name", name)}, nil, 0)
	upl := uploadID(t, rec)
	rec = upSession(t, fx.s, fx.sess, "PATCH", "/api/v1/uploads/"+upl, map[string]string{
		"Content-Type": uploadBodyType, "Upload-Offset": "0"}, bytes.NewReader(data), int64(len(data)))
	wantStatus(t, rec, http.StatusNoContent)
	return upl
}

// start asks for an import of the upload into the target folder, and returns the answer.
func (fx *importFixture) start(t *testing.T, upl, onConflict string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"source": "archive", "upload": upl, "folder": fx.folder}
	if onConflict != "" {
		body["on_conflict"] = onConflict
	}
	return callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body)
}

// startOK asks for an import and returns it, queued.
func (fx *importFixture) startOK(t *testing.T, upl, onConflict string) importJSON {
	t.Helper()
	rec := fx.start(t, upl, onConflict)
	wantStatus(t, rec, http.StatusAccepted)
	return decode[importJSON](t, rec)
}

// get reads an import as the user.
func (fx *importFixture) get(t *testing.T, impID string) importJSON {
	t.Helper()
	rec := callSession(t, fx.s, fx.sess, "GET", "/api/v1/imports/"+impID, nil)
	wantStatus(t, rec, http.StatusOK)
	return decode[importJSON](t, rec)
}

// runJob claims the queued import job and runs it once, as the pool's worker does, and returns what the run returned.
func (fx *importFixture) runJob(t *testing.T) error {
	t.Helper()
	ctx := context.Background()
	job, err := fx.s.store.ClaimJobOf(ctx, jobImport)
	if err != nil {
		t.Fatalf("no import job is queued: %v", err)
	}
	runErr := fx.s.runImport(ctx, job)
	if errors.Is(runErr, jobs.ErrYield) {
		if err := fx.s.store.YieldJob(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
		return runErr
	}
	if err := fx.s.store.FinishJob(ctx, job.ID, runErr); err != nil {
		t.Fatal(err)
	}
	return runErr
}

// children lists the top level of the target folder, as the user sees it.
func (fx *importFixture) children(t *testing.T) map[string]nodeJSON {
	t.Helper()
	rec := callSession(t, fx.s, fx.sess, "GET", "/api/v1/folders/"+fx.folder+"/children", nil)
	wantStatus(t, rec, http.StatusOK)
	out := map[string]nodeJSON{}
	for _, n := range decode[listResponse[nodeJSON]](t, rec).Items {
		out[n.Name] = n
	}
	return out
}

// auditActions returns the audit actions of the account, read from the store with every entry shown. The HTTP audit log
// does not show a personal folder's entries to the account's admin (decision 18), so the test reads the store.
func (fx *importFixture) auditActions(t *testing.T) []string {
	t.Helper()
	entries, _, err := fx.s.store.AuditLog(context.Background(), fx.acc.ID, fx.user.ID, true, store.Page{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	return actions
}

// zipArchive builds a zip of the entries in order; a name ending in "/" is a folder.
func zipArchive(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, e[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tarGzSymlink builds a gzipped tar holding a symbolic link, which an import refuses.
func tarGzSymlink(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var photosZip = [][2]string{{"photos/", ""}, {"photos/a.jpg", "AAA"}, {"notes.txt", "hello"}}

// TestImportZipCopiesTheTreeAndAudits: a zip with a folder and two files is copied into the target, the folder as a
// folder, each file with its audit entry, and the import is done with its counts.
func TestImportZipCopiesTheTreeAndAudits(t *testing.T) {
	fx := newImportFixture(t)
	upl := fx.archive(t, "photos.zip", zipArchive(t, photosZip))
	imp := fx.startOK(t, upl, "")
	if imp.State != store.ImportQueued {
		t.Fatalf("state = %s, want queued", imp.State)
	}
	if err := fx.runJob(t); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := fx.get(t, imp.ID)
	if got.State != store.ImportDone || got.Files != 2 || got.Entries != 3 {
		t.Fatalf("import = %+v, want done with 2 files of 3 entries", got)
	}
	top := fx.children(t)
	if _, ok := top["photos"]; !ok || top["photos"].Kind != store.KindDir {
		t.Fatalf("top level = %v, want the photos folder", top)
	}
	if _, ok := top["notes.txt"]; !ok {
		t.Fatalf("top level = %v, want notes.txt", top)
	}
	actions := strings.Join(fx.auditActions(t), ",")
	for _, want := range []string{"import.start", "import.finish", "node.create"} {
		if !strings.Contains(actions, want) {
			t.Errorf("audit actions %s do not hold %s", actions, want)
		}
	}
}

// TestImportZipSlipIsRefusedAtCreation: a zip whose entry climbs out of the folder is refused when the import is asked
// for, with its code, and no import is made.
func TestImportZipSlipIsRefusedAtCreation(t *testing.T) {
	fx := newImportFixture(t)
	upl := fx.archive(t, "evil.zip", zipArchive(t, [][2]string{{"../escape.txt", "x"}}))
	rec := fx.start(t, upl, "")
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid_path") {
		t.Fatalf("answer = %s, want invalid_path", rec.Body.String())
	}
	if n := len(fx.children(t)); n != 0 {
		t.Fatalf("top level has %d entries after a refused import", n)
	}
}

// TestImportTarLinkFailsCleanly: a tar holding a link is refused by the job, which fails the import with the code and
// removes what it staged. The target is left as it was.
func TestImportTarLinkFailsCleanly(t *testing.T) {
	fx := newImportFixture(t)
	upl := fx.archive(t, "links.tar.gz", tarGzSymlink(t))
	imp := fx.startOK(t, upl, "")
	if err := fx.runJob(t); err == nil {
		t.Fatal("the job of a tar with a link succeeded")
	}
	got := fx.get(t, imp.ID)
	if got.State != store.ImportFailed || got.ErrorCode != "invalid_path" {
		t.Fatalf("import = %+v, want failed with invalid_path", got)
	}
	if n := len(fx.children(t)); n != 0 {
		t.Fatalf("top level has %d entries after a failed import", n)
	}
}

// TestImportRestartResumesAndCopiesNothingTwice: a run that stops after the first entry resumes from its cursor, keeps
// the entry it finished, and makes each file once. A second import of the same archive copies no file again.
func TestImportRestartResumesAndCopiesNothingTwice(t *testing.T) {
	fx := newImportFixture(t)
	entries, yielded := 0, false
	fx.s.importFault = func(stage string) error {
		if stage == "entry" {
			entries++
			if entries == 2 && !yielded {
				yielded = true
				return jobs.ErrYield
			}
		}
		return nil
	}
	data := zipArchive(t, [][2]string{{"a.txt", "1"}, {"b.txt", "2"}, {"c.txt", "3"}})
	upl := fx.archive(t, "three.zip", data)
	imp := fx.startOK(t, upl, "")
	if err := fx.runJob(t); !errors.Is(err, jobs.ErrYield) {
		t.Fatalf("first run = %v, want a yield", err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportRunning || got.Files != 1 {
		t.Fatalf("after the yield: %+v, want running with the first file kept", got)
	}
	if err := fx.runJob(t); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportDone || got.Files != 3 {
		t.Fatalf("after resume: %+v, want done with 3 files", got)
	}
	if n := len(fx.children(t)); n != 3 {
		t.Fatalf("top level has %d entries, want 3", n)
	}
	fx.s.importFault = nil

	again := fx.startOK(t, fx.archive(t, "three-again.zip", data), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("second import: %v", err)
	}
	got := fx.get(t, again.ID)
	if got.State != store.ImportDone || got.Files != 0 || got.Skipped != 3 {
		t.Fatalf("second import = %+v, want done with 3 skipped and no files", got)
	}
	if n := len(fx.children(t)); n != 3 {
		t.Fatalf("top level has %d entries after the second import, want 3", n)
	}
}

// TestImportPauseResumeAndCancel: a paused import stays paused when its job runs; a resume queues it again and it
// completes; a cancelled import removes what it staged and is recorded as cancelled.
func TestImportPauseResumeAndCancel(t *testing.T) {
	fx := newImportFixture(t)
	imp := fx.startOK(t, fx.archive(t, "p.zip", zipArchive(t, photosZip)), "")
	rec := callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/pause", nil)
	wantStatus(t, rec, http.StatusOK)
	if err := fx.runJob(t); err != nil {
		t.Fatalf("a paused import's job ends: %v", err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportPaused {
		t.Fatalf("state = %s, want paused", got.State)
	}
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+imp.ID+"/resume", nil), http.StatusOK)
	if err := fx.runJob(t); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportDone {
		t.Fatalf("after resume: %s, want done", got.State)
	}

	cancelled := fx.startOK(t, fx.archive(t, "c.zip", zipArchive(t, photosZip)), "")
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports/"+cancelled.ID+"/cancel", nil), http.StatusOK)
	if err := fx.runJob(t); err != nil {
		t.Fatalf("cancelled run: %v", err)
	}
	got := fx.get(t, cancelled.ID)
	if got.State != store.ImportCancelled || got.FinishedAt == nil {
		t.Fatalf("cancelled import = %+v", got)
	}
	if !strings.Contains(strings.Join(fx.auditActions(t), ","), "import.cancel") {
		t.Fatal("no import.cancel audit entry")
	}
}

// TestImportNameClashFollowsOnConflict: a file that is already in the target is kept under fail (the import fails),
// and gets a numbered name under rename (the default).
func TestImportNameClashFollowsOnConflict(t *testing.T) {
	fx := newImportFixture(t)
	first := fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"notes.txt", "one"}})), "")
	if err := fx.runJob(t); err != nil || fx.get(t, first.ID).State != store.ImportDone {
		t.Fatalf("first import: %v", err)
	}

	clash := zipArchive(t, [][2]string{{"notes.txt", "two"}})
	failing := fx.startOK(t, fx.archive(t, "two.zip", clash), "fail")
	if err := fx.runJob(t); err == nil {
		t.Fatal("a fail import over an existing name succeeded")
	}
	if got := fx.get(t, failing.ID); got.State != store.ImportFailed || got.ErrorCode != "conflict" {
		t.Fatalf("fail import = %+v, want failed with conflict", got)
	}

	renamed := fx.startOK(t, fx.archive(t, "three.zip", clash), "")
	if err := fx.runJob(t); err != nil {
		t.Fatalf("rename import: %v", err)
	}
	if got := fx.get(t, renamed.ID); got.State != store.ImportDone {
		t.Fatalf("rename import = %+v", got)
	}
	top := fx.children(t)
	// The numbering is the one every rename uses ("name (1).ext", as freeName makes it).
	if _, ok := top["notes (1).txt"]; !ok {
		t.Fatalf("top level = %v, want notes (1).txt", top)
	}
}

// TestImportRightsAndTheAccountSwitch: another user cannot read or change an import; the owner turns the archive source
// off for the account, and an import is refused while it is off.
func TestImportRightsAndTheAccountSwitch(t *testing.T) {
	fx := newImportFixture(t)
	imp := fx.startOK(t, fx.archive(t, "mine.zip", zipArchive(t, photosZip)), "")

	_, otherSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "bob@example.com")
	rec := callSession(t, fx.s, otherSess, "GET", "/api/v1/imports/"+imp.ID, nil)
	wantStatus(t, rec, http.StatusNotFound)
	rec = callSession(t, fx.s, otherSess, "POST", "/api/v1/imports/"+imp.ID+"/pause", nil)
	wantStatus(t, rec, http.StatusNotFound)

	// An archive is refused at its upload while the source is off, so the one for the refused import is made first.
	offUpl := fx.archive(t, "off.zip", zipArchive(t, photosZip))
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/imports/archive", fx.key,
		map[string]any{"enabled": false}), http.StatusOK)
	wantStatus(t, fx.start(t, offUpl, ""), http.StatusForbidden)
	rec = upSession(t, fx.s, fx.sess, "POST", "/api/v1/uploads", map[string]string{
		"Upload-Length": "1", "Upload-Metadata": md("kind", "import", "name", "late.zip")}, nil, 0)
	wantStatus(t, rec, http.StatusForbidden)
}

// TestImportEventCarriesIDsAndCountsOnly: the payload of an import event holds IDs and counts, and no name.
func TestImportEventCarriesIDsAndCountsOnly(t *testing.T) {
	b, err := json.Marshal(webhook.Import{ID: "imp_x", Source: "archive", FolderID: "fld_x", Entries: 3, Files: 2,
		Bytes: 9, Skipped: 1, ErrorCode: "quota_exceeded"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		switch k {
		case "id", "source", "folder_id", "entries", "files", "bytes", "skipped", "error_code":
		default:
			t.Fatalf("import event has key %q, which is not an ID or a count", k)
		}
	}
}

// TestImportQuotaAndRightsAreCheckedAtCreation: an archive that does not fit the account's quota is refused when the
// import is asked for, and so is a target the caller may not write to (a team space where the caller is not a member).
func TestImportQuotaAndRightsAreCheckedAtCreation(t *testing.T) {
	fx := newImportFixture(t)
	// The photos archive declares 8 bytes of files, so a quota of 4 refuses it.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 4}), http.StatusOK)
	rec := fx.start(t, fx.archive(t, "big.zip", zipArchive(t, photosZip)), "")
	if rec.Code == http.StatusAccepted || !strings.Contains(rec.Body.String(), "quota_exceeded") {
		t.Fatalf("answer = %d %s, want quota_exceeded refused at creation", rec.Code, rec.Body.String())
	}
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID, fx.admin,
		map[string]any{"quota_bytes": 0}), http.StatusOK)

	team := newFolder(t, fx.s, fx.key, fx.acc.ID, "Team docs")
	upl := fx.archive(t, "team.zip", zipArchive(t, photosZip))
	body := map[string]any{"source": "archive", "upload": upl, "folder": team.ID}
	wantStatus(t, callSession(t, fx.s, fx.sess, "POST", "/api/v1/imports", body), http.StatusNotFound)
}
