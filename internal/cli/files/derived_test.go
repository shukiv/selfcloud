// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// The derived commands are checked against a fake that answers as their routes do: a thumbnail that is pending for a
// while, an export that is made in the background, and then a ready file. The same commands are checked against the
// real server in the probe package.

var testExport = id.Derive("drv", "cli11-export")

func noDerivedWait(t *testing.T) {
	t.Helper()
	oldThumb, oldExport := thumbSleep, exportSleep
	t.Cleanup(func() { thumbSleep, exportSleep = oldThumb, oldExport })
	thumbSleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	exportSleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
}

func pendingThumb(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "2")
	writeDoc(w, http.StatusAccepted, map[string]string{"state": "pending"})
}

func serveThumb(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", `"thumb"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func serveExport(w http.ResponseWriter, r *http.Request, data []byte, name, etag string) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", "application/pdf")
	if name != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

// TestThumbnailPollsUntilReadyAndWritesTheFile: the size is asked for, a pending answer is followed, and the JPEG is
// written with the two output lines.
func TestThumbnailPollsUntilReadyAndWritesTheFile(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	data := []byte("\xff\xd8thumbnail-bytes")
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			pendingThumb(w)
			return
		}
		serveThumb(w, data)
	})
	dest := filepath.Join(t.TempDir(), "small.jpg")
	res := f.run(Thumbnail, testNode, dest, "--size", "256")
	wantCode(t, res, 0)
	if res.stdout != "saved="+dest+"\nsize="+strconv.Itoa(len(data))+"\n" {
		t.Errorf("stdout %q", res.stdout)
	}
	if got, err := os.ReadFile(dest); err != nil || !bytes.Equal(got, data) {
		t.Errorf("file %q, %v", got, err)
	}
	reqs := f.seen()
	if len(reqs) != 2 || reqs[0].method != "GET" || reqs[0].path != "/api/v1/nodes/"+testNode+"/thumbnail" ||
		reqs[0].query != "size=256" {
		t.Fatalf("requests %+v", reqs)
	}
}

// TestThumbnailRetryAfterFromARefusal: a 429 gives the wait, then the next poll answers with the image.
func TestThumbnailRetryAfterFromARefusal(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			w.Header().Set("Retry-After", "3")
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "")
			return
		}
		serveThumb(w, []byte("jpeg"))
	})
	dest := filepath.Join(t.TempDir(), "x.jpg")
	res := f.run(Thumbnail, testNode, dest)
	wantCode(t, res, 0)
	if got, err := os.ReadFile(dest); err != nil || string(got) != "jpeg" {
		t.Errorf("file %q, %v", got, err)
	}
}

// TestThumbnailSignedLinkIsRefused: a 303 is not followed, and the message says why.
func TestThumbnailSignedLinkIsRefused(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Location", "https://elsewhere.test/thumb")
		w.WriteHeader(http.StatusSeeOther)
	})
	dest := filepath.Join(t.TempDir(), "x.jpg")
	res := f.run(Thumbnail, testNode, dest)
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "this server hands thumbnails out through signed links") {
		t.Errorf("stderr %q", res.stderr)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("destination exists: %v", err)
	}
}

// TestThumbnailTimeout: a thumbnail that stays pending ends with the message and exit 1.
func TestThumbnailTimeout(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { pendingThumb(w) })
	res := f.run(Thumbnail, testNode, filepath.Join(t.TempDir(), "x.jpg"), "--wait-timeout", "1ns")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "the thumbnail is not ready yet") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestThumbnailRefusesBeforeRequests: a taken destination and a bad size cost no call.
func TestThumbnailRefusesBeforeRequests(t *testing.T) {
	isolate(t, testAccountKey)
	dir := t.TempDir()
	dest := filepath.Join(dir, "x.jpg")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { serveThumb(w, []byte("new")) })
	res := f.run(Thumbnail, testNode, dest)
	wantCode(t, res, 2)
	if len(f.seen()) != 0 {
		t.Fatalf("requests %+v", f.seen())
	}
	res = f.run(Thumbnail, testNode, filepath.Join(dir, "other.jpg"), "--size", "0")
	wantCode(t, res, 2)
	if len(f.seen()) != 0 {
		t.Fatalf("requests %+v", f.seen())
	}
}

// TestThumbnailDirectoryAndForce: a directory gets thumbnail.jpg, and --force replaces a regular file.
func TestThumbnailDirectoryAndForce(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { serveThumb(w, []byte("new")) })
	dir := t.TempDir()
	res := f.run(Thumbnail, testNode, dir)
	wantCode(t, res, 0)
	if got, err := os.ReadFile(filepath.Join(dir, "thumbnail.jpg")); err != nil || string(got) != "new" {
		t.Errorf("file %q, %v", got, err)
	}
	dest := filepath.Join(dir, "x.jpg")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = f.run(Thumbnail, testNode, dest, "--force")
	wantCode(t, res, 0)
	if got, err := os.ReadFile(dest); err != nil || string(got) != "new" {
		t.Errorf("file %q, %v", got, err)
	}
}

// TestThumbnailToStdout: - writes the JPEG to standard output and the lines to standard error.
func TestThumbnailToStdout(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) { serveThumb(w, []byte("jpeg")) })
	res := f.run(Thumbnail, testNode, "-")
	wantCode(t, res, 0)
	if res.stdout != "jpeg" {
		t.Errorf("stdout %q", res.stdout)
	}
	if !strings.Contains(res.stderr, "saved=-\nsize=4\n") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestExportPollsAndDownloads: the format is posted once, the export is followed, and its file is downloaded.
func TestExportPollsAndDownloads(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	data := []byte("the exported document")
	etag := `"` + sha(data) + `"`
	gets := 0
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch r.Method {
		case http.MethodPost:
			writeDoc(w, http.StatusAccepted, map[string]any{"id": testExport, "state": "queued", "format": "pdf"})
		case http.MethodGet:
			gets++
			switch gets {
			case 1:
				w.Header().Set("Retry-After", "5")
				writeDoc(w, http.StatusAccepted, map[string]string{"state": "pending"})
			case 2:
				w.Header().Set("Retry-After", "5")
				writeErr(w, http.StatusServiceUnavailable, "unavailable", "")
			default:
				serveExport(w, r, data, "report.pdf", etag)
			}
		}
	})
	dest := filepath.Join(t.TempDir(), "out.pdf")
	res := f.run(Export, testNode, "--format", "pdf", dest)
	wantCode(t, res, 0)
	if res.stdout != "saved="+dest+"\nsize="+strconv.Itoa(len(data))+"\n" {
		t.Errorf("stdout %q", res.stdout)
	}
	if got, err := os.ReadFile(dest); err != nil || !bytes.Equal(got, data) {
		t.Errorf("file %q, %v", got, err)
	}
	reqs := f.seen()
	if len(reqs) != 1+3 {
		t.Fatalf("requests %+v", reqs)
	}
	if reqs[0].method != "POST" || reqs[0].path != "/api/v1/nodes/"+testNode+"/exports" ||
		reqs[0].body != `{"format":"pdf"}` {
		t.Errorf("post %+v", reqs[0])
	}
	for _, r := range reqs[1:] {
		if r.method != "GET" || r.path != "/api/v1/nodes/"+testNode+"/exports/"+testExport {
			t.Errorf("poll %+v", r)
		}
	}
	if reqs[1].rng != "bytes=0-0" || reqs[3].ifRange != etag {
		t.Errorf("ranges %+v", reqs)
	}
}

// TestExportDefaultName: with no destination, the server's name is used in the current directory.
func TestExportDefaultName(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	dir := t.TempDir()
	t.Chdir(dir)
	data := []byte("document")
	etag := `"` + sha(data) + `"`
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Method == http.MethodPost {
			writeDoc(w, http.StatusAccepted, map[string]any{"id": testExport, "state": "queued", "format": "pdf"})
			return
		}
		serveExport(w, r, data, "report.pdf", etag)
	})
	res := f.run(Export, testNode, "--format", "pdf")
	wantCode(t, res, 0)
	if got, err := os.ReadFile(filepath.Join(dir, "report.pdf")); err != nil || !bytes.Equal(got, data) {
		t.Errorf("file %q, %v", got, err)
	}
}

// TestExportFailedExport: a 415 whose reason is failed is a failure, not a usage error.
func TestExportFailedExport(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeErr(w, http.StatusUnsupportedMediaType, "unprocessable", "failed")
	})
	res := f.run(Export, testNode, "--format", "pdf", filepath.Join(t.TempDir(), "x.pdf"))
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "the server says unprocessable") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestExportValidation: a format outside the list and a bad destination cost no request.
func TestExportValidation(t *testing.T) {
	isolate(t, testAccountKey)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeDoc(w, http.StatusAccepted, map[string]any{"id": testExport})
	})
	res := f.run(Export, testNode, "--format", "jpeg", filepath.Join(t.TempDir(), "x.jpg"))
	wantCode(t, res, 2)
	dir := t.TempDir()
	dest := filepath.Join(dir, "x.pdf")
	if err := os.WriteFile(dest, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = f.run(Export, testNode, "--format", "pdf", dest)
	wantCode(t, res, 2)
	if len(f.seen()) != 0 {
		t.Fatalf("requests %+v", f.seen())
	}
}

// TestExportTimeout: an export that stays pending ends with the message and exit 1.
func TestExportTimeout(t *testing.T) {
	isolate(t, testAccountKey)
	noDerivedWait(t)
	f := newZipFake(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Method == http.MethodPost {
			writeDoc(w, http.StatusAccepted, map[string]any{"id": testExport, "state": "queued", "format": "pdf"})
			return
		}
		w.Header().Set("Retry-After", "5")
		writeDoc(w, http.StatusAccepted, map[string]string{"state": "pending"})
	})
	res := f.run(Export, testNode, "--format", "pdf", filepath.Join(t.TempDir(), "x.pdf"),
		"--wait-timeout", "1ns")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "not ready") {
		t.Errorf("stderr %q", res.stderr)
	}
}
