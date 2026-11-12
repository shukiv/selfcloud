// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The zip commands against the real server: it makes the zips with its own job runner, serves them in ranges, and
// answers a zip that is gone as a zip that does not exist.

// startJobs runs the server's job runner for the length of the test, so a zip is made.
func (l *live) startJobs() {
	l.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		l.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = l.s.Serve(ctx, ln, nil)
	}()
	l.t.Cleanup(func() {
		cancel()
		<-done
	})
}

// runAt calls a command against another address of the same server.
func (l *live) runAt(url string, fn func([]string, io.Writer, io.Writer) int, args ...string) result {
	l.t.Helper()
	var out, errOut bytes.Buffer
	code := fn(append(append([]string{}, args...), "--url", url), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// madeZip makes a zip of nodes through the plain address, waits for it, and returns its id.
func (l *live) madeZip(nodes ...string) string {
	l.t.Helper()
	res := l.runAt(l.plain, ZipCreate, append(nodes, "--wait", "--wait-timeout", "2m")...)
	if res.code != 0 {
		l.t.Fatalf("zip create --wait: exit %d, stdout %q, stderr %q", res.code, res.stdout, res.stderr)
	}
	return valueOfLine(l.t, res.stdout, "zip=")
}

// TestZipLiveCreateWaitGetHoldsTheTree: a folder with two files becomes a zip that opens as one, named after the folder,
// with each file's bytes in it, and the download's SHA-256 is checked.
func TestZipLiveCreateWaitGetHoldsTheTree(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	l.startJobs()
	a, b := randomBytes(t, 3000), []byte("second file")
	l.upload("/Reports", "a.bin", a, "fail")
	l.upload("/Reports", "b.txt", b, "fail")
	dir := l.child("Reports")
	res := l.run(ZipCreate, dir, "--wait", "--wait-timeout", "2m")
	l.wantCode(res, 0)
	if !strings.Contains(res.stdout, "state=ready") || !strings.Contains(res.stdout, "skipped=0") {
		t.Errorf("stdout %q", res.stdout)
	}
	zipID := valueOfLine(t, res.stdout, "zip=")
	out := t.TempDir()
	res = l.run(ZipGet, zipID, out)
	l.wantCode(res, 0)
	if !strings.Contains(res.stdout, "checked=sha256") {
		t.Errorf("stdout %q", res.stdout)
	}
	raw, err := os.ReadFile(filepath.Join(out, "Reports.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("the download is not a zip: %v", err)
	}
	got := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(rc)
		rc.Close()
		got[f.Name] = buf.Bytes()
	}
	if !bytes.Equal(got["Reports/a.bin"], a) || !bytes.Equal(got["Reports/b.txt"], b) {
		t.Errorf("entries %v", len(got))
	}
}

// TestZipLiveCreateByPath: a path in --folder names the node, as get does.
func TestZipLiveCreateByPath(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	l.startJobs()
	l.upload("/", "one.txt", []byte("one"), "fail")
	res := l.run(ZipCreate, "/one.txt", "--folder", l.folder, "--wait", "--wait-timeout", "2m")
	l.wantCode(res, 0)
	if sizeLine := valueOfLine(t, res.stdout, "size="); sizeLine == "" || sizeLine == "0" {
		t.Errorf("stdout %q", res.stdout)
	}
}

// TestZipLiveGetResumesAfterThreeCuts: the zip route serves ranges with a strong ETag, so a download cut three times
// comes back identical to one that was not cut, and a range after a cut starts where the part file ended.
func TestZipLiveGetResumesAfterThreeCuts(t *testing.T) {
	cut := newCutter(true, -1, 3*mib/2, 5*mib/4, 13*mib/10)
	l := newLive(t, nil, cut)
	l.isolate()
	l.startJobs()
	noWait(t)
	nodeID := l.upload("/", "cut.bin", randomBytes(t, 5*mib), "fail")
	zipID := l.madeZip(nodeID)
	resp, whole := l.call(http.MethodGet, "/api/v1/zips/"+zipID, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the zip itself: %d", resp.StatusCode)
	}
	dest := t.TempDir()
	res := l.run(ZipGet, zipID, dest, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	if cut.count() != 3 {
		t.Fatalf("%d cuts, want 3", cut.count())
	}
	got, err := os.ReadFile(filepath.Join(dest, "cut.bin.zip"))
	if err != nil || sha(got) != sha(whole) {
		t.Fatalf("the downloaded zip differs after the cuts: %v", err)
	}
	last, midRange := int64(-1), false
	for _, c := range l.rec.all() {
		if c.method != http.MethodGet || !strings.HasSuffix(c.path, zipID) || c.rng == "bytes=0-0" || c.rng == "" {
			continue
		}
		start := rangeStart(c.rng)
		if start <= last {
			t.Errorf("a range starts at %d, not after %d: bytes that arrived were fetched again", start, last)
		}
		if start%mib != 0 {
			midRange = true
		}
		last = start
	}
	if !midRange {
		t.Error("no range started inside a piece: a resume must start at the part file's size")
	}
}

// TestZipLiveRmRemovesTheZip: after zip rm the zip is not there, for get or for another rm.
func TestZipLiveRmRemovesTheZip(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	l.startJobs()
	zipID := l.madeZip(l.upload("/", "gone.txt", []byte("bytes"), "fail"))
	l.wantCode(l.run(ZipRm, zipID, "--yes"), 0)
	l.wantCode(l.run(ZipGet, zipID, t.TempDir()), 4)
	l.wantCode(l.run(ZipRm, zipID, "--yes"), 4)
}

// TestZipLiveCreateRefusesAShortcut: the server's refusal of a shortcut comes back with its reason and exit 2.
func TestZipLiveCreateRefusesAShortcut(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	target := l.upload("/", "target.txt", []byte("t"), "fail")
	l.upload("/d", "x.txt", []byte("x"), "fail")
	dirNode := l.child("d")
	out := l.jsonCall(http.MethodPost, "/api/v1/nodes/"+dirNode+"/shortcuts", map[string]string{"target": target})
	v, err := decodeNode(out)
	if err != nil {
		t.Fatal(err)
	}
	res := l.run(ZipCreate, v.ID)
	l.wantCode(res, 2)
	if !strings.Contains(res.stderr, "shortcut") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestZipLiveGetOfAZipNotReady: a zip that is queued answers 202, so get says it is not ready and writes nothing. The
// job runner is not started, so the zip stays queued.
func TestZipLiveGetOfAZipNotReady(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	nodeID := l.upload("/", "slow.txt", []byte("s"), "fail")
	res := l.run(ZipCreate, nodeID)
	l.wantCode(res, 0)
	zipID := valueOfLine(t, res.stdout, "zip=")
	dest := t.TempDir()
	res = l.run(ZipGet, zipID, dest)
	l.wantCode(res, 1)
	if !strings.Contains(res.stderr, "not ready") || len(dirEntries(t, dest)) != 0 {
		t.Errorf("stderr %q, files %v", res.stderr, dirEntries(t, dest))
	}
	res = l.run(ZipRm, zipID, "--yes")
	l.wantCode(res, 3)
	if !strings.Contains(res.stderr, "still being made") {
		t.Errorf("stderr %q", res.stderr)
	}
}
