// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	mrand "math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// zipFixture is sharingFixture with a configuration the test chooses.
func zipFixture(t *testing.T, mutate ...func(*config.Config)) *sharingFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, mutate...)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fx := &sharingFixture{s: s, acc: acc, key: key, admin: admin}
	fx.fld = newFolder(t, s, key, acc.ID, "Team")
	fx.a = mkdirs(t, s, key, fx.fld.ID, "/a")
	fx.b = mkdirs(t, s, key, fx.fld.ID, "/a/b")
	fx.c = mkdirs(t, s, key, fx.fld.ID, "/a/c")
	mkdirs(t, s, key, fx.fld.ID, "/a/empty")
	fx.deep = putNode(t, s, key, fx.fld.ID, "/a/b/file.txt", "deep")
	fx.sib = putNode(t, s, key, fx.fld.ID, "/a/c/x.txt", "sib")
	fx.ann, fx.annSess = verifiedUser(t, s, key, acc.ID, "ann@example.com")
	fx.bob, fx.bobSess = verifiedUser(t, s, key, acc.ID, "bob@example.com")
	return fx
}

// createZip asks for a zip of nodes, as the account key (sess nil) or as a session, and wants it accepted.
func createZip(t *testing.T, fx *sharingFixture, sess *testSession, nodes ...string) jobJSON {
	t.Helper()
	body := map[string]any{"nodes": nodes}
	var rec *httptest.ResponseRecorder
	if sess != nil {
		b, _ := json.Marshal(body)
		rec = rawSession(t, fx.s, *sess, "POST", "/api/v1/zips", string(b), map[string]string{"Content-Type": "application/json"})
	} else {
		rec = call(t, fx.s, "POST", "/api/v1/zips", fx.key, body)
	}
	wantStatus(t, rec, 202)
	return decode[jobJSON](t, rec)
}

// runZip runs the queue once and returns the job as stored.
func runZip(t *testing.T, fx *sharingFixture, jobID string) store.Job {
	t.Helper()
	if err := fx.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	j, err := fx.s.store.Job(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	return *j
}

// getZip downloads a zip as the account key (sess nil) or as a session, with an optional Range header.
func getZip(t *testing.T, fx *sharingFixture, sess *testSession, zipID string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	if sess != nil {
		return rawSession(t, fx.s, *sess, "GET", "/api/v1/zips/"+zipID, "", hdr)
	}
	return rawCall(t, fx.s, "GET", "/api/v1/zips/"+zipID, fx.key, "", hdr)
}

// zipEntries reads a zip body and returns its entries by name, with their contents and UTF-8 flags.
func zipEntries(t *testing.T, body []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip does not read back: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		if f.NonUTF8 {
			t.Errorf("entry %q does not carry the UTF-8 flag", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(b)
	}
	return out
}

func TestZipRoundTripKeepsTheTreeAndNames(t *testing.T) {
	fx := zipFixture(t)
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/café.txt", "cafe")
	job := createZip(t, fx, nil, fx.a.ID)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	rec := getZip(t, fx, nil, job.Target, nil)
	wantStatus(t, rec, 200)
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "a.zip") {
		t.Errorf("disposition %q does not name the zip after its root", cd)
	}
	if etag := rec.Header().Get("ETag"); len(etag) != 66 {
		t.Errorf("ETag %q is not a quoted SHA-256", etag)
	}
	got := zipEntries(t, rec.Body.Bytes())
	want := map[string]string{"a/": "", "a/b/": "", "a/c/": "", "a/empty/": "", "a/b/file.txt": "deep",
		"a/c/x.txt": "sib", "a/café.txt": "cafe"}
	if len(got) != len(want) {
		t.Errorf("entries %v, want %d", got, len(want))
	}
	for name, body := range want {
		if g, ok := got[name]; !ok || g != body {
			t.Errorf("entry %q = %q (present %v), want %q", name, g, ok, body)
		}
	}
	if rec.Header().Get("X-Selfcloud-Skipped") != "0" {
		t.Errorf("skipped header %q", rec.Header().Get("X-Selfcloud-Skipped"))
	}
}

func TestZipDownloadResumesWithRange(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	runZip(t, fx, job.ID)
	full := getZip(t, fx, nil, job.Target, nil).Body.Bytes()
	rec := getZip(t, fx, nil, job.Target, map[string]string{"Range": "bytes=10-"})
	wantStatus(t, rec, 206)
	if !bytes.Equal(rec.Body.Bytes(), full[10:]) {
		t.Errorf("ranged body is not the rest of the zip")
	}
	wantStatus(t, getZip(t, fx, nil, job.Target, map[string]string{"Range": "bytes=999999999-"}), 416)
}

func TestZipIsForItsMakerAndCheckedEveryCall(t *testing.T) {
	fx := zipFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	job := createZip(t, fx, &fx.annSess, fx.a.ID)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	wantStatus(t, getZip(t, fx, &fx.annSess, job.Target, nil), 200)
	// Another member of the account, and an account of another owner, get the answer of a zip that does not exist.
	wantStatus(t, getZip(t, fx, &fx.bobSess, job.Target, nil), 404)
	wantStatus(t, getZip(t, fx, nil, job.Target, nil), 404)
	admin := adminKey(t, fx.s)
	_, otherKey := newAccount(t, fx.s, admin, "Other")
	wantStatus(t, rawCall(t, fx.s, "GET", "/api/v1/zips/"+job.Target, otherKey, "", nil), 404)
	// A second credential of the same user is not the maker. A session's zip is read by no token, and a token's zip is
	// read only by that token: another token of the same user, which has the same user and folders, is refused too.
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	wantStatus(t, rawCall(t, fx.s, "GET", "/api/v1/zips/"+job.Target, tok, "", nil), 404)
	tokJob := createZipWithCred(t, fx, tok, fx.a.ID)
	if j := runZip(t, fx, tokJob.ID); j.Status != "succeeded" {
		t.Fatalf("token zip %s: %s", j.Status, j.Error)
	}
	wantStatus(t, rawCall(t, fx.s, "GET", "/api/v1/zips/"+tokJob.Target, tok, "", nil), 200)
	wantStatus(t, getZip(t, fx, &fx.annSess, tokJob.Target, nil), 404)
	wantStatus(t, getZip(t, fx, nil, tokJob.Target, nil), 404)
	time.Sleep(2 * time.Millisecond)
	other := mintToken(t, fx.s, fx.key, fx.acc.ID, fx.fld.ID, fx.ann.ID)
	wantStatus(t, rawCall(t, fx.s, "GET", "/api/v1/zips/"+tokJob.Target, other, "", nil), 404)
	// Revoked access refuses the download, Range resume included.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.a.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
	wantStatus(t, getZip(t, fx, &fx.annSess, job.Target, nil), 404)
	wantStatus(t, getZip(t, fx, &fx.annSess, job.Target, map[string]string{"Range": "bytes=1-"}), 404)
}

func TestZipFollowsTrashAndDeleteRefusesWhilePending(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.b.ID)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/zips/"+job.Target, fx.key, nil), 409)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
	wantStatus(t, getZip(t, fx, nil, job.Target, nil), 404)
}

func TestZipDeleteRemovesTheFile(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	runZip(t, fx, job.ID)
	path, err := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("zip file missing before delete: %v", err)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/zips/"+job.Target, fx.key, nil), 204)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("zip file still there after delete: %v", err)
	}
}

func TestZipRefusesRestrictedRootsAndCountsShortcuts(t *testing.T) {
	fx := zipFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.a.ID+"/shortcuts", fx.key,
		map[string]any{"target": fx.sib.ID}), 201)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.b.ID, fx.key, map[string]any{"no_download": true}), 200)
	// The viewer may not zip a tree with a restricted node in it: a copy's rule.
	rec := createZipAs(t, fx, &fx.annSess, fx.a.ID)
	wantStatus(t, rec, 403)
	if !strings.Contains(rec.Body.String(), "download_restricted") {
		t.Errorf("refusal %s does not name download_restricted", rec.Body.String())
	}
	// The account key is a manager of the tree and is not bound by the rule.
	job := createZip(t, fx, nil, fx.a.ID)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	owner := getZip(t, fx, nil, job.Target, nil)
	got := zipEntries(t, owner.Body.Bytes())
	if _, ok := got["a/b/file.txt"]; !ok {
		t.Errorf("owner's zip lost a file: %v", got)
	}
	// The shortcut in /a is left out and counted, never followed into the file it points to.
	if owner.Header().Get("X-Selfcloud-Skipped") != "1" {
		t.Errorf("skipped %q, want 1", owner.Header().Get("X-Selfcloud-Skipped"))
	}
	if len(got) != 6 {
		t.Errorf("entries %v, want 6 (no shortcut)", got)
	}
}

// createZipAs is createZip without the wanted status: the answer is the caller's.
func createZipAs(t *testing.T, fx *sharingFixture, sess *testSession, nodes ...string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"nodes": nodes})
	return rawSession(t, fx.s, *sess, "POST", "/api/v1/zips", string(b), map[string]string{"Content-Type": "application/json"})
}

func TestZipLimitsRefuseBeforeWriting(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) { c.Zip.MaxEntries = 3 })
	job := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, job.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "entries") {
		t.Fatalf("job %s %q, want failed on the entry limit", j.Status, j.Error)
	}
	if _, err := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target); err != nil {
		t.Fatalf("the zip row: %v", err)
	}
	z, _ := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
	if z.State != store.ZipFailed {
		t.Errorf("zip state %q, want failed", z.State)
	}
	path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
	part, _ := fx.s.zipFiles.PartPath(fx.acc.ID, job.Target)
	for _, p := range []string{path, part} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after a refused zip", p)
		}
	}
}

// incompressible returns n bytes that deflate does not shrink, so a zip's size is its contents' size.
func incompressible(n int) string {
	b := make([]byte, n)
	mrand.New(mrand.NewSource(int64(n))).Read(b)
	return string(b)
}

// TestZipDiskBudgetCountsReadyAndReserved: the stored zips share zip.max_disk. A ready zip holds its size, and an
// unfinished one holds its snapshot's bytes; a zip that would pass the budget is refused before it writes.
func TestZipDiskBudgetCountsReadyAndReserved(t *testing.T) {
	fx := zipFixture(t, func(c *config.Config) {
		c.Zip.MaxBytes = 1 << 20
		c.Zip.MaxDisk = 1 << 20
		c.Zip.MaxDiskPerAccount = 1 << 20
	})
	mkdirs(t, fx.s, fx.key, fx.fld.ID, "/big")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/big/one.bin", incompressible(600<<10))
	first := createZip(t, fx, nil, rootOf(t, fx, fx.fld.ID))
	if j := runZip(t, fx, first.ID); j.Status != "succeeded" {
		t.Fatalf("first zip %s: %s", j.Status, j.Error)
	}
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/two.bin", incompressible(600<<10))
	second := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, second.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "disk budget") {
		t.Fatalf("second zip %s %q, want failed on the disk budget", j.Status, j.Error)
	}
	path, _ := fx.s.zipFiles.PartPath(fx.acc.ID, second.Target)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a refused zip left %s", path)
	}
}

func TestZipCountsUnfinishedPerCaller(t *testing.T) {
	fx := zipFixture(t)
	createZip(t, fx, nil, fx.a.ID)
	createZip(t, fx, nil, fx.b.ID)
	rec := call(t, fx.s, "POST", "/api/v1/zips", fx.key, map[string]any{"nodes": []string{fx.c.ID}})
	wantStatus(t, rec, 409)
	if !strings.Contains(rec.Body.String(), "too_many_zips") {
		t.Errorf("refusal %s does not name too_many_zips", rec.Body.String())
	}
}

func TestZipRequestRefusals(t *testing.T) {
	fx := zipFixture(t)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/zips", fx.key, map[string]any{"nodes": []string{}}), 400)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/zips", fx.key, map[string]any{"nodes": []string{"../etc"}}), 400)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/zips", fx.key,
		map[string]any{"nodes": []string{fx.a.ID, fx.a.ID}}), 400)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/zips", fx.key,
		map[string]any{"nodes": []string{fx.a.ID}, "extra": 1}), 400)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/zips", "", map[string]any{"nodes": []string{fx.a.ID}}), 401)
}

func TestZipYieldsItsTurnAndResumes(t *testing.T) {
	fx := zipFixture(t)
	fx.s.zipYield = 0
	job := createZip(t, fx, nil, fx.a.ID)
	stored, err := fx.s.store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	yields, done := 0, false
	for i := 0; i < 100 && !done; i++ {
		err := fx.s.runZipJob(context.Background(), stored)
		if err == nil {
			done = true
			break
		}
		if err != jobs.ErrYield {
			t.Fatalf("turn %d: %v", i, err)
		}
		yields++
	}
	if !done {
		t.Fatalf("the zip did not finish within 100 turns (%d yields): a turn makes no progress", yields)
	}
	if yields < 2 {
		t.Fatalf("the zip yielded %d times, want several with a zero budget", yields)
	}
	wantStatus(t, getZip(t, fx, nil, job.Target, nil), 200)
}

func TestZipCrashStartsOverAndLeavesNothing(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	stored, err := fx.s.store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entries := 0
	fx.s.zipFault = func(stage string) error {
		if stage == "entry" {
			entries++
			if entries == 2 {
				cancel() // a shutdown in the middle of the zip
			}
		}
		return nil
	}
	if err := fx.s.runZipJob(ctx, stored); err == nil {
		t.Fatal("a shutdown mid-zip returned no error")
	}
	z, _ := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
	if z.State != store.ZipPending {
		t.Fatalf("after a shutdown the zip is %q, want pending", z.State)
	}
	// The process restarts: the in-memory work is gone, and the job makes the zip again from the snapshot.
	fx.s.zipFault = nil
	fx.s.zips.drop(job.Target)
	if err := fx.s.runZipJob(context.Background(), stored); err != nil {
		t.Fatalf("restart: %v", err)
	}
	got := zipEntries(t, getZip(t, fx, nil, job.Target, nil).Body.Bytes())
	if got["a/b/file.txt"] != "deep" || len(got) != 6 {
		t.Errorf("restarted zip holds %v", got)
	}
}

func TestZipFaultFailsTheJobAndRemovesItsFiles(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	fx.s.zipFault = func(stage string) error {
		if stage == "entry" {
			return context.DeadlineExceeded
		}
		return nil
	}
	j := runZip(t, fx, job.ID)
	if j.Status != "failed" {
		t.Fatalf("job %s, want failed", j.Status)
	}
	fx.s.zipFault = nil
	z, _ := fx.s.store.Zip(context.Background(), fx.acc.ID, job.Target)
	if z.State != store.ZipFailed {
		t.Fatalf("zip state %q, want failed", z.State)
	}
	part, _ := fx.s.zipFiles.PartPath(fx.acc.ID, job.Target)
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Errorf("partial file left behind: %v", err)
	}
}

func TestZipDetectsAContentsMismatch(t *testing.T) {
	fx := zipFixture(t)
	n := fx.deep
	node, err := fx.s.store.Node(context.Background(), fx.acc.ID, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Change the blob's bytes in place, keeping its size: the checksum the node holds no longer matches.
	var found string
	_ = filepath.WalkDir(fx.s.cfg.DataDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == node.BlobID {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatal("blob file not found")
	}
	if err := os.WriteFile(found, []byte("DEEP"), 0o600); err != nil {
		t.Fatal(err)
	}
	job := createZip(t, fx, nil, fx.a.ID)
	j := runZip(t, fx, job.ID)
	if j.Status != "failed" || !strings.Contains(j.Error, "checksum") {
		t.Fatalf("job %s %q, want failed on the checksum", j.Status, j.Error)
	}
}

func TestZipSweepRemovesExpiredAndAbandoned(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	runZip(t, fx, job.ID)
	path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
	future := store.Now().Add(fx.s.cfg.Zip.TTL.D() + time.Minute)
	res, err := fx.s.zipFiles.Sweep(context.Background(), future, 100)
	if err != nil || res.Expired != 1 {
		t.Fatalf("sweep %+v %v, want one expired zip", res, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expired zip file still there")
	}
	wantStatus(t, getZip(t, fx, nil, job.Target, nil), 404)
	// A pending zip whose job has ended without making it is failed by the sweep, and its partial file removed.
	abandoned := createZip(t, fx, nil, fx.b.ID)
	if err := fx.s.store.FinishJob(context.Background(), abandoned.ID, errors.New("lost")); err != nil {
		t.Fatal(err)
	}
	res, err = fx.s.zipFiles.Sweep(context.Background(), future, 100)
	if err != nil || res.Abandoned != 1 {
		t.Fatalf("sweep %+v %v, want one abandoned zip", res, err)
	}
	// The failed row is past zip.ttl at the sweep's time, so the same pass removes it.
	if _, err := fx.s.store.Zip(context.Background(), fx.acc.ID, abandoned.Target); err != store.ErrNotFound {
		t.Errorf("abandoned zip after the ttl: %v, want removed as a failed row", err)
	}
}

func TestZipAccountDeletionRemovesItsFiles(t *testing.T) {
	fx := zipFixture(t)
	job := createZip(t, fx, nil, fx.a.ID)
	runZip(t, fx, job.ID)
	path, _ := fx.s.zipFiles.Path(fx.acc.ID, job.Target)
	if err := fx.s.zipFiles.RemoveAccount(fx.acc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("account's zip file survived its deletion")
	}
}

// TestZip64EntriesAndCountPastTheLimits reads back a zip with more than 65,535 entries, written with the job's header
// settings (archive/zip, deflate). The 4 GiB case through the job's own path is TestZip64ThroughTheJob (zips_measure_test.go).
func TestZip64EntriesAndCountPastTheLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	const entries = 65_536 + 4
	for i := 0; i < entries; i++ {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: "d/f" + strconv.Itoa(i), Method: zip.Deflate}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("zip with %d entries does not read back: %v", entries, err)
	}
	if len(zr.File) != entries {
		t.Errorf("read %d entries, wrote %d", len(zr.File), entries)
	}
	zr.Close()
}

type zipZeros struct{}

func (zipZeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestZipComponentsAreSafe(t *testing.T) {
	cases := map[string]string{
		"plain.txt":   "plain.txt",
		"a/b":         "a_b",
		"a\\b":        "a_b",
		"C:evil":      "C_evil",
		"..":          "_",
		".":           "_",
		"":            "_",
		"‮evil.txt":   "_evil.txt",
		"x​y":         "x_y",
		"tab\there":   "tab_here",
		"nul\x00in":   "nul_in",
		"line sep":    "line_sep",
		"café":       "café", // decomposed becomes NFC
		"  spaced  ":  "  spaced  ",
		"ok (1).txt":  "ok (1).txt",
		"back\x7fdel": "back_del",
		"..‮x/../..":  ".._x_.._..",
	}
	for in, want := range cases {
		got := zipComponent(in)
		if got != want {
			t.Errorf("zipComponent(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(got, "/\\:") || got == "." || got == ".." {
			t.Errorf("zipComponent(%q) = %q is not one safe element", in, got)
		}
	}
	long := zipComponent(strings.Repeat("é", 200))
	if len(long) > 255 || !utf8.ValidString(long) {
		t.Errorf("long name: %d bytes, valid %v", len(long), utf8.ValidString(long))
	}
	if got := zipComponent(strings.Repeat("a", 300)); len(got) != 255 {
		t.Errorf("300 ASCII bytes became %d", len(got))
	}
}

func TestZipSiblingNamesStayDistinct(t *testing.T) {
	ns := nameSet{}
	if got := ns.unique("Report.txt"); got != "Report.txt" {
		t.Errorf("first = %q", got)
	}
	if got := ns.unique("report.TXT"); got == "report.TXT" || !strings.HasSuffix(got, " (1).TXT") {
		t.Errorf("a name that folds to an existing one = %q", got)
	}
	if got := ns.unique("Report.txt"); got != "Report (2).txt" {
		t.Errorf("third = %q", got)
	}
	if got := ns.unique("folder"); got != "folder" {
		t.Errorf("no extension: %q", got)
	}
	if got := ns.unique("folder"); got != "folder (1)" {
		t.Errorf("no extension, second: %q", got)
	}
}

func TestZipRootsWithTheSameNameStayDistinct(t *testing.T) {
	fx := zipFixture(t)
	x := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/x")
	y := mkdirs(t, fx.s, fx.key, fx.fld.ID, "/y")
	px := putNode(t, fx.s, fx.key, fx.fld.ID, "/x/Report.txt", "one")
	py := putNode(t, fx.s, fx.key, fx.fld.ID, "/y/Report.txt", "two")
	job := createZip(t, fx, nil, px.ID, py.ID)
	if j := runZip(t, fx, job.ID); j.Status != "succeeded" {
		t.Fatalf("job %s: %s", j.Status, j.Error)
	}
	got := zipEntries(t, getZip(t, fx, nil, job.Target, nil).Body.Bytes())
	if got["Report.txt"] != "one" || got["Report (1).txt"] != "two" {
		t.Errorf("roots collided: %v", got)
	}
	_, _ = x, y
}

// createZipWithCred asks for a zip with a bearer credential, and wants it accepted.
func createZipWithCred(t *testing.T, fx *sharingFixture, cred string, nodes ...string) jobJSON {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/zips", cred, map[string]any{"nodes": nodes})
	wantStatus(t, rec, 202)
	return decode[jobJSON](t, rec)
}
