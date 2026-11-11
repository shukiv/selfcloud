// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
)

// goldenRecords writes two upload records with fixed values.
func goldenRecords(t *testing.T) (a, b uploadRecord) {
	t.Helper()
	a = uploadRecord{ID: id.Derive("upl", "cli6-golden-a"), Server: "https://drive.example", Profile: "home",
		Local: "/data/films/a.mkv", Size: 5 << 30, Sent: 1 << 30, Folder: testFolder, Path: "/films", Name: "a.mkv",
		OnConflict: "fail", StartedAt: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC), LocalModified: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	b = uploadRecord{ID: id.Derive("upl", "cli6-golden-b"), Server: "http://127.0.0.1:8080", Local: "/tmp/b.iso",
		Size: 3 << 20, Sent: 0, Folder: testFolder, Parent: testNode, Name: "b.iso", OnConflict: "replace",
		StartedAt: time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC),
		LocalModified: time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)}
	for _, r := range []uploadRecord{b, a} {
		if err := saveUploadRecord(r); err != nil {
			t.Fatal(err)
		}
	}
	return a, b
}

// TestUploadLsGolden: upload ls lists the unfinished uploads this client keeps, oldest first, as a table and as JSON.
// It reads only the local records, so it needs no server and no credential.
func TestUploadLsGolden(t *testing.T) {
	isolate(t, "")
	a, b := goldenRecords(t)
	var out, errOut strings.Builder
	if code := UploadLs(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	want := table(
		[]string{"ID", "SENT", "SIZE", "EXPIRES", "SERVER", "LOCAL"},
		[]string{a.ID, "1073741824", "5368709120", "2026-11-11T09:30:00Z", "https://drive.example", "/data/films/a.mkv"},
		[]string{b.ID, "0", "3145728", "2026-11-11T10:00:00Z", "http://127.0.0.1:8080", "/tmp/b.iso"},
	)
	if out.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()
	if code := UploadLs([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("--json: exit %d", code)
	}
	wantJSON := `[
  {
    "id": "` + a.ID + `",
    "server": "https://drive.example",
    "profile": "home",
    "local": "/data/films/a.mkv",
    "folder": "` + testFolder + `",
    "path": "/films",
    "name": "a.mkv",
    "on_conflict": "fail",
    "size": 5368709120,
    "sent": 1073741824,
    "started_at": "2026-11-10T09:00:00Z",
    "expires_at": "2026-11-11T09:30:00Z"
  },
  {
    "id": "` + b.ID + `",
    "server": "http://127.0.0.1:8080",
    "local": "/tmp/b.iso",
    "folder": "` + testFolder + `",
    "parent": "` + testNode + `",
    "name": "b.iso",
    "on_conflict": "replace",
    "size": 3145728,
    "sent": 0,
    "started_at": "2026-11-10T10:00:00Z",
    "expires_at": "2026-11-11T10:00:00Z"
  }
]
`
	if out.String() != wantJSON {
		t.Errorf("--json:\n%s\nwant:\n%s", out.String(), wantJSON)
	}
}

// TestUploadLsEmptyAndUnreadable: no records is a header alone; a record that cannot be read is skipped with a note that
// does not repeat its contents.
func TestUploadLsEmptyAndUnreadable(t *testing.T) {
	isolate(t, "")
	var out, errOut strings.Builder
	if code := UploadLs(nil, &out, &errOut); code != 0 || out.String() != table([]string{"ID", "SENT", "SIZE", "EXPIRES", "SERVER", "LOCAL"}) {
		t.Fatalf("exit %d, stdout %q", code, out.String())
	}
	a, _ := goldenRecords(t)
	bad := filepath.Join(uploadsDir(t), id.Derive("upl", "cli6-garbage")+".json")
	if err := os.WriteFile(bad, []byte("not json \x1b[31m"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := UploadLs(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), a.ID) || strings.Count(out.String(), "upl_") != 2 {
		t.Errorf("stdout %q", out.String())
	}
	if !strings.Contains(errOut.String(), "could not be read") || strings.Contains(errOut.String(), "\x1b") {
		t.Errorf("stderr %q", errOut.String())
	}
}

// blockedPut sends one piece of a 3 MiB file, then loses the server, so the upload stays open and recorded.
func blockedPut(t *testing.T, mutate func(*config.Config)) (*live, *cutter, string, string) {
	t.Helper()
	cut := newCutter(false, 0, 3*mib/2)
	l := newLive(t, mutate, cut)
	l.isolate()
	noWait(t)
	local := writeLocal(t, "open.bin", randomBytes(t, 3*mib))
	res := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	l.wantCode(res, 1)
	cut.setAfter(-1)
	return l, cut, uploadIDIn(t, res.stderr), local
}

// TestUploadCancelEndsTheUpload: upload cancel ends the upload on the server (tus termination) and forgets it here.
func TestUploadCancelEndsTheUpload(t *testing.T) {
	l, _, upl, local := blockedPut(t, nil)
	var out, errOut strings.Builder
	if code := UploadLs(nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), upl) ||
		!strings.Contains(out.String(), local) || !strings.Contains(out.String(), "1048576") {
		t.Fatalf("upload ls before the cancel: exit %d, %q", code, out.String())
	}
	res := l.run(UploadCancel, upl)
	l.wantCode(res, 0)
	if !hasRow(res.stdout, upl, "done") {
		t.Errorf("stdout %q", res.stdout)
	}
	if n := countCalls(l.rec.all(), "DELETE /api/v1/uploads/{upl}"); n != 1 {
		t.Errorf("%d terminations", n)
	}
	if r := records(t); len(r) != 0 {
		t.Errorf("the cancelled upload is still recorded: %v", r)
	}
	resp, _ := l.call(http.MethodHead, "/api/v1/uploads/"+upl, http.Header{"Tus-Resumable": {"1.0.0"}}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD after the cancel: %d, want 404", resp.StatusCode)
	}
	wantCode(t, l.run(UploadCancel, "upl_bad"), 2)
	wantCode(t, l.run(UploadCancel), 2)
}

// TestUploadCancelOfAnExpiredUploadExits6: the server says the upload expired; the command exits 6 and forgets it.
func TestUploadCancelOfAnExpiredUploadExits6(t *testing.T) {
	l, _, upl, _ := blockedPut(t, func(c *config.Config) { c.Upload.MaxLifetime = config.Duration(time.Second) })
	time.Sleep(1500 * time.Millisecond)
	res := l.run(UploadCancel, upl)
	l.wantCode(res, 6)
	if r := records(t); len(r) != 0 {
		t.Errorf("the expired upload is still recorded: %v", r)
	}
}

// TestUploadRecordIsPrivate: the record is 0600 in 0700 directories, and holds ids, paths and sizes, no credential.
func TestUploadRecordIsPrivate(t *testing.T) {
	l, _, upl, local := blockedPut(t, nil)
	p := filepath.Join(uploadsDir(t), upl+".json")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("record mode %04o, want 0600", fi.Mode().Perm())
	}
	for _, d := range []string{uploadsDir(t), filepath.Dir(uploadsDir(t))} {
		if di, err := os.Stat(d); err != nil || di.Mode().Perm() != 0o700 {
			t.Errorf("%s: mode %v, want 0700", d, di.Mode().Perm())
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), l.key) || strings.Contains(string(b), "sc_") {
		t.Errorf("the record holds a credential: %s", b)
	}
	if !strings.Contains(string(b), upl) || !strings.Contains(string(b), local) {
		t.Errorf("the record does not name the upload and the file: %s", b)
	}
}

// TestUploadCommandsRefuseASharedProfilesDirectory: the records live in the profiles directory, so its privacy rule
// applies to them too.
func TestUploadCommandsRefuseASharedProfilesDirectory(t *testing.T) {
	isolate(t, "")
	goldenRecords(t)
	dir := os.Getenv("SELFCLOUD_PROFILES_DIR")
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	if code := UploadLs(nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "chmod 700") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}
