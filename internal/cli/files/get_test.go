// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// contentGets returns the content requests the record saw.
func contentGets(calls []seen) []seen {
	var out []seen
	for _, c := range calls {
		if c.method == http.MethodGet && strings.HasSuffix(c.path, "/content") {
			out = append(out, c)
		}
	}
	return out
}

// rangeStart reads the first byte of a "bytes=a-b" header, or -1.
func rangeStart(h string) int64 {
	v, ok := strings.CutPrefix(h, "bytes=")
	if !ok {
		return -1
	}
	a, _, _ := strings.Cut(v, "-")
	n, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// dirEntries lists the names in dir.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// downloadsDir is where the download records are kept.
func downloadsDir() string {
	return filepath.Join(os.Getenv("SELFCLOUD_PROFILES_DIR"), "transfers", "downloads")
}

// downloadRecords lists the download record files.
func downloadRecords(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(downloadsDir())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestGetDownloadsAFileInRanges: get reads the file in ranges of --piece-size, each with If-Range set to the file's
// version, writes it under the server's name, checks its SHA-256 and says so, and leaves no part file or record.
func TestGetDownloadsAFileInRanges(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	data := randomBytes(t, 3*mib+mib/2)
	nodeID := l.upload("/", "ranges.bin", data, "fail")
	etag := l.stat(nodeID).ETag
	dest := t.TempDir()
	res := l.run(Get, nodeID, dest, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	got, err := os.ReadFile(filepath.Join(dest, "ranges.bin"))
	if err != nil || sha(got) != sha(data) {
		t.Fatalf("the downloaded file differs: %v", err)
	}
	if !strings.Contains(res.stdout, "checked=sha256") || !strings.Contains(res.stdout, "sha256="+sha(data)) {
		t.Errorf("stdout %q does not say the SHA-256 was checked", res.stdout)
	}
	gets := contentGets(l.rec.all())
	wantRanges := []string{"bytes=0-1048575", "bytes=1048576-2097151", "bytes=2097152-3145727", "bytes=3145728-3670015"}
	if len(gets) != len(wantRanges) {
		t.Fatalf("%d ranges, want %d", len(gets), len(wantRanges))
	}
	for i, g := range gets {
		if g.rng != wantRanges[i] || g.ifRange != etag || g.status != http.StatusPartialContent {
			t.Errorf("range %d: Range %q If-Range %q status %d", i, g.rng, g.ifRange, g.status)
		}
	}
	if names := dirEntries(t, dest); len(names) != 1 {
		t.Errorf("the destination holds %v, want the file alone", names)
	}
	if r := downloadRecords(t); len(r) != 0 {
		t.Errorf("download records left: %v", r)
	}
}

// TestGetResumesFromThePartFileAfterThreeCuts is the fault-injection proof for downloads (design-cli section 6): a 5 MiB
// file in 1 MiB ranges comes back identical after three cut connections. Each range after a cut starts at the size of
// the part file, so the bytes that arrived are not fetched again. The budgets are smaller than put's: a download keeps
// the bytes of a cut range, so less is sent again after each cut.
func TestGetResumesFromThePartFileAfterThreeCuts(t *testing.T) {
	cut := newCutter(true, -1, 3*mib/2, 5*mib/4, 13*mib/10)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	data := randomBytes(t, 5*mib)
	nodeID := l.upload("/", "cut.bin", data, "fail")
	dest := t.TempDir()
	res := l.run(Get, nodeID, dest, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	if cut.count() != 3 {
		t.Fatalf("%d cuts, want 3", cut.count())
	}
	got, err := os.ReadFile(filepath.Join(dest, "cut.bin"))
	if err != nil || sha(got) != sha(data) {
		t.Fatalf("the downloaded file differs after the cuts: %v", err)
	}
	gets := contentGets(l.rec.all())
	last, midRange := int64(-1), false
	for i, g := range gets {
		start := rangeStart(g.rng)
		if start <= last {
			t.Errorf("range %d starts at %d, not after %d: bytes that arrived were fetched again", i, start, last)
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

// TestGetNeverMixesAFileThatChangedDuringAResume: the file is replaced while a download is cut. The resume asks with
// If-Range set to the version it began with, so the server sends the whole new file, and get starts again on it and
// says why. The result is the new file, never the old start with the new end.
func TestGetNeverMixesAFileThatChangedDuringAResume(t *testing.T) {
	cut := newCutter(true, -1, 3*mib/2)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	v1, v2 := randomBytes(t, 5*mib), randomBytes(t, 5*mib)
	nodeID := l.upload("/", "changing.bin", v1, "fail")
	etag1 := l.stat(nodeID).ETag
	var hookErr error
	var once sync.Once
	cut.onCut = func(int) {
		once.Do(func() { hookErr = replaceRaw(l, "changing.bin", v2) })
	}
	dest := t.TempDir()
	res := l.run(Get, nodeID, dest, "--piece-size", "1MiB")
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	l.wantCode(res, 0)
	got, err := os.ReadFile(filepath.Join(dest, "changing.bin"))
	if err != nil || sha(got) != sha(v2) {
		t.Fatalf("the result is not the new file (mixed: %v): %v", sha(got) != sha(v1) && sha(got) != sha(v2), err)
	}
	if !strings.Contains(res.stderr, "changed") {
		t.Errorf("stderr %q does not say the file changed", res.stderr)
	}
	gets := contentGets(l.rec.all())
	if len(gets) < 2 || gets[1].ifRange != etag1 {
		t.Errorf("the resume after the cut did not ask with the first version's If-Range: %+v", gets)
	}
}

// replaceRaw replaces a file's content in the folder's root through the plain address, as another person would. It
// runs inside the server's connection, so it reports a failure instead of failing the test.
func replaceRaw(l *live, name string, data []byte) error {
	meta := "folder " + b64(l.folder) + ",name " + b64(name) + ",on_conflict " + b64("replace")
	req, err := http.NewRequest(http.MethodPost, l.plain+"/api/v1/uploads", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+l.key)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", strconv.Itoa(len(data)))
	req.Header.Set("Upload-Metadata", meta)
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("replace: %d %s", resp.StatusCode, b)
	}
	return nil
}

// TestGetResumesAcrossRunsFromThePart: a download that gives up keeps its part file; the next get of the same file to
// the same place goes on from the part's size, with the version it began with.
func TestGetResumesAcrossRunsFromThePart(t *testing.T) {
	cut := newCutter(true, 0, 3*mib/2)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	data := randomBytes(t, 4*mib)
	nodeID := l.upload("/", "later.bin", data, "fail")
	etag := l.stat(nodeID).ETag
	dest := t.TempDir()
	first := l.run(Get, nodeID, dest, "--piece-size", "1MiB")
	l.wantCode(first, 1)
	if !strings.Contains(first.stderr, "later.bin.part") {
		t.Errorf("the failure does not name the part file it kept: %q", first.stderr)
	}
	fi, err := os.Stat(filepath.Join(dest, "later.bin.part"))
	if err != nil || fi.Size() == 0 {
		t.Fatalf("no part file kept: %v", err)
	}
	held := fi.Size()
	cut.setAfter(-1)
	before := len(contentGets(l.rec.all()))
	second := l.run(Get, nodeID, dest, "--piece-size", "1MiB")
	l.wantCode(second, 0)
	gets := contentGets(l.rec.all())[before:]
	if len(gets) == 0 || rangeStart(gets[0].rng) != held || gets[0].ifRange != etag {
		t.Errorf("the second run's first range: %+v; want it to start at %d with If-Range %s", gets, held, etag)
	}
	got, err := os.ReadFile(filepath.Join(dest, "later.bin"))
	if err != nil || sha(got) != sha(data) {
		t.Fatalf("the resumed file differs: %v", err)
	}
}

// TestGetAcrossRunsStartsAgainWhenTheFileChanged: when the file changed between two runs, the part file is not
// continued: get says so and starts from zero on the new version.
func TestGetAcrossRunsStartsAgainWhenTheFileChanged(t *testing.T) {
	cut := newCutter(true, 0, 3*mib/2)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	v1, v2 := randomBytes(t, 4*mib), randomBytes(t, 4*mib)
	nodeID := l.upload("/", "moved-on.bin", v1, "fail")
	dest := t.TempDir()
	l.wantCode(l.run(Get, nodeID, dest, "--piece-size", "1MiB"), 1)
	if err := replaceRaw(l, "moved-on.bin", v2); err != nil {
		t.Fatal(err)
	}
	cut.setAfter(-1)
	before := len(contentGets(l.rec.all()))
	res := l.run(Get, nodeID, dest, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	if !strings.Contains(res.stderr, "changed") {
		t.Errorf("stderr %q does not say the file changed", res.stderr)
	}
	gets := contentGets(l.rec.all())[before:]
	if len(gets) == 0 || rangeStart(gets[0].rng) != 0 {
		t.Errorf("the second run did not start from zero: %+v", gets)
	}
	got, err := os.ReadFile(filepath.Join(dest, "moved-on.bin"))
	if err != nil || sha(got) != sha(v2) {
		t.Fatalf("the result is not the new version: %v", err)
	}
}

// fileServer is a fake API for get: one node, whose name, SHA-256 and bytes a test chooses, served with ranges.
type fileServer struct {
	t       *testing.T
	srv     *httptest.Server
	nodeID  string
	name    string
	kind    string
	sha     string
	data    []byte
	mu      sync.Mutex
	content int // content requests
}

func newFileServer(t *testing.T, name string, data []byte, digest string) *fileServer {
	t.Helper()
	f := &fileServer{t: t, nodeID: id.Derive("nod", "cli6-get-"+name), name: name, kind: "file", sha: digest, data: data}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

const fakeETag = `"0123456789abcdef0123456789abcdef"`

func (f *fileServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/nodes/" + f.nodeID:
		doc := node(f.nodeID, f.name, f.kind)
		doc["size"] = len(f.data)
		doc["etag"] = fakeETag
		if f.sha != "" {
			doc["sha256"] = f.sha
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, jsonOf(doc))
	case "/api/v1/nodes/" + f.nodeID + "/content":
		f.mu.Lock()
		f.content++
		f.mu.Unlock()
		w.Header().Set("ETag", fakeETag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.data))
	default:
		http.NotFound(w, r)
	}
}

func (f *fileServer) get(args ...string) result {
	f.t.Helper()
	var out, errOut bytes.Buffer
	code := Get(append(append([]string{}, args...), "--url", f.srv.URL), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func (f *fileServer) contentCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.content
}

// TestGetRefusesHostileNames: the name is the server's, so it is checked before anything is written: a name that climbs,
// holds a separator or a NUL, is empty, is . or .., is a reserved device name on any platform, or is too long to take
// .part, is refused (exit 2). No byte is fetched, and nothing appears in the destination or beside it.
func TestGetRefusesHostileNames(t *testing.T) {
	isolate(t, testAccountKey)
	for _, name := range []string{"../x", "a/b", "..", ".", "", "a\x00b", "CON", "con.txt", "Lpt1", "NUL.tar.gz",
		"COM9", `a\b`, strings.Repeat("n", 252)} {
		f := newFileServer(t, name, []byte("abc"), sha([]byte("abc")))
		parent := t.TempDir()
		dest := filepath.Join(parent, "in")
		if err := os.Mkdir(dest, 0o700); err != nil {
			t.Fatal(err)
		}
		res := f.get(f.nodeID, dest)
		wantCode(t, res, 2)
		if f.contentCalls() != 0 {
			t.Errorf("%q: the content was fetched", name)
		}
		if got := dirEntries(t, dest); len(got) != 0 {
			t.Errorf("%q: the destination holds %v", name, got)
		}
		if got := dirEntries(t, parent); len(got) != 1 {
			t.Errorf("%q: beside the destination: %v", name, got)
		}
	}
}

// TestGetNeverFollowsASymlinkAtTheDestination: a symbolic link at the file's name or at its part file's name is
// refused, with or without --overwrite, and the file it points to is left as it was.
func TestGetNeverFollowsASymlinkAtTheDestination(t *testing.T) {
	isolate(t, testAccountKey)
	for _, at := range []string{"f.txt", "f.txt.part"} {
		for _, overwrite := range []bool{false, true} {
			f := newFileServer(t, "f.txt", []byte("server bytes"), sha([]byte("server bytes")))
			outside := filepath.Join(t.TempDir(), "victim")
			if err := os.WriteFile(outside, []byte("keep me"), 0o600); err != nil {
				t.Fatal(err)
			}
			dest := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(dest, at)); err != nil {
				t.Fatal(err)
			}
			args := []string{f.nodeID, dest}
			if overwrite {
				args = append(args, "--overwrite")
			}
			res := f.get(args...)
			wantCode(t, res, 2)
			if b, _ := os.ReadFile(outside); string(b) != "keep me" {
				t.Errorf("%s, overwrite %v: the link's target was written: %q", at, overwrite, b)
			}
			if fi, err := os.Lstat(filepath.Join(dest, at)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("%s, overwrite %v: the link was replaced", at, overwrite)
			}
		}
	}
}

// TestGetNeverOverwritesWithoutTheFlag: a file at the name is kept unless --overwrite is given, and then it is replaced
// only once the new bytes are complete and checked.
func TestGetNeverOverwritesWithoutTheFlag(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFileServer(t, "keep.txt", []byte("new bytes"), sha([]byte("new bytes")))
	dest := t.TempDir()
	target := filepath.Join(dest, "keep.txt")
	if err := os.WriteFile(target, []byte("old bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.get(f.nodeID, dest), 2)
	if f.contentCalls() != 0 {
		t.Error("the content was fetched for a destination that exists")
	}
	if b, _ := os.ReadFile(target); string(b) != "old bytes" {
		t.Fatalf("the file was overwritten without --overwrite: %q", b)
	}
	wantCode(t, f.get(f.nodeID, dest, "--overwrite"), 0)
	if b, _ := os.ReadFile(target); string(b) != "new bytes" {
		t.Errorf("--overwrite left %q", b)
	}
	// A destination that names a new file takes that name.
	named := filepath.Join(dest, "chosen.txt")
	wantCode(t, f.get(f.nodeID, named), 0)
	if b, _ := os.ReadFile(named); string(b) != "new bytes" {
		t.Errorf("the named destination holds %q", b)
	}
}

// TestGetChecksumMismatchExits5: bytes that do not match the server's SHA-256 are not kept: exit 5, no file, no part
// file and no record.
func TestGetChecksumMismatchExits5(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFileServer(t, "bad.bin", []byte("abd"), sha([]byte("abc")))
	dest := t.TempDir()
	res := f.get(f.nodeID, dest)
	wantCode(t, res, 5)
	if !strings.Contains(res.stderr, "SHA-256") {
		t.Errorf("stderr %q does not name the check", res.stderr)
	}
	if got := dirEntries(t, dest); len(got) != 0 {
		t.Errorf("the destination holds %v after a failed check", got)
	}
	if r := downloadRecords(t); len(r) != 0 {
		t.Errorf("download records left: %v", r)
	}
}

// TestGetSaysWhichCheckItMade: with no SHA-256 from the server, get checks the size and says so.
func TestGetSaysWhichCheckItMade(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFileServer(t, "plain.txt", []byte("hello"), "")
	dest := t.TempDir()
	res := f.get(f.nodeID, dest)
	wantCode(t, res, 0)
	if !strings.Contains(res.stdout, "checked=size") || !strings.Contains(res.stderr, "size") {
		t.Errorf("stdout %q, stderr %q: the size check is not named", res.stdout, res.stderr)
	}
	jres := f.get(f.nodeID, filepath.Join(dest, "again.txt"), "--json")
	wantCode(t, jres, 0)
	if !strings.Contains(jres.stdout, `"checked": "size"`) {
		t.Errorf("--json: %q", jres.stdout)
	}
}

// TestGetRefusesAStrayPartFile: a part file that no download of this client began is not continued: get refuses it, or
// starts again over it with --overwrite.
func TestGetRefusesAStrayPartFile(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFileServer(t, "stray.bin", []byte("0123456789"), sha([]byte("0123456789")))
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "stray.bin.part"), []byte("XXXX"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.get(f.nodeID, dest), 2)
	if f.contentCalls() != 0 {
		t.Error("the content was fetched over a stray part file")
	}
	wantCode(t, f.get(f.nodeID, dest, "--overwrite"), 0)
	if b, _ := os.ReadFile(filepath.Join(dest, "stray.bin")); string(b) != "0123456789" {
		t.Errorf("the file holds %q", b)
	}
}

// getAt runs get against the server at url.
func getAt(url string, args ...string) result {
	var out, errOut bytes.Buffer
	code := Get(append(append([]string{}, args...), "--url", url), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// writeNode answers a stat of a file: its name, size, version and SHA-256.
func writeNode(w http.ResponseWriter, nodeID, name string, data []byte, etag string) {
	doc := node(nodeID, name, "file")
	doc["size"] = len(data)
	doc["etag"] = etag
	doc["sha256"] = sha(data)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, jsonOf(doc))
}

// TestGetSaysWhenRangesAreIgnored: a server, or a proxy in front of it, that answers a range with the whole file of the
// same version has not changed the file: get stops at once (exit 1) and says what happened, rather than start again.
func TestGetSaysWhenRangesAreIgnored(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFileServer(t, "ignored.bin", bytes.Repeat([]byte("m"), 3000), "")
	whole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			r.Header.Del("Range")
		}
		f.handle(w, r)
	}))
	t.Cleanup(whole.Close)
	res := getAt(whole.URL, f.nodeID, t.TempDir(), "--piece-size", "1MiB")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "instead of a range") || strings.Contains(res.stderr, "changed") {
		t.Errorf("stderr %q does not say that the range was ignored", res.stderr)
	}
	if strings.Contains(res.stderr, "get again") {
		t.Errorf("stderr %q says to run get again, which meets the same answer", res.stderr)
	}
	if n := f.contentCalls(); n != 1 {
		t.Errorf("%d content requests, want 1", n)
	}
}

// TestGetStopsWhenTheFileKeepsChanging: a file that is a new version at every range makes get start again three times,
// then stop (exit 1) and say why, rather than loop.
func TestGetStopsWhenTheFileKeepsChanging(t *testing.T) {
	isolate(t, testAccountKey)
	nodeID := id.Derive("nod", "cli6-moving")
	data := bytes.Repeat([]byte("m"), 3000)
	var mu sync.Mutex
	version, contents := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/nodes/" + nodeID:
			writeNode(w, nodeID, "moving.bin", data, fmt.Sprintf(`"v%d"`, version))
		case "/api/v1/nodes/" + nodeID + "/content":
			contents++
			version++
			w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, version))
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	res := getAt(srv.URL, nodeID, t.TempDir(), "--piece-size", "1MiB")
	wantCode(t, res, 1)
	if !strings.Contains(res.stderr, "kept changing") {
		t.Errorf("stderr %q does not say the file kept changing", res.stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if contents != maxRestarts+1 {
		t.Errorf("%d content requests, want %d", contents, maxRestarts+1)
	}
}

// TestGetRefusesADirectory: get takes a file.
func TestGetRefusesADirectory(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFileServer(t, "photos", nil, "")
	f.kind = "dir"
	wantCode(t, f.get(f.nodeID, t.TempDir()), 2)
	if f.contentCalls() != 0 {
		t.Error("a directory's content was fetched")
	}
}

// TestGetEmptyFileAndByPath: a file of no bytes downloads as an empty file, and a path in --folder finds its node.
func TestGetEmptyFileAndByPath(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	l.upload("/", "empty.txt", nil, "fail")
	data := randomBytes(t, 1000)
	l.upload("/docs/2026", "deep.bin", data, "fail")
	dest := t.TempDir()
	res := l.run(Get, "--folder", l.folder, "/empty.txt", dest)
	l.wantCode(res, 0)
	if fi, err := os.Stat(filepath.Join(dest, "empty.txt")); err != nil || fi.Size() != 0 {
		t.Fatalf("empty file: %v", err)
	}
	res = l.run(Get, "--folder", l.folder, "/docs/2026/deep.bin", dest)
	l.wantCode(res, 0)
	if got, err := os.ReadFile(filepath.Join(dest, "deep.bin")); err != nil || sha(got) != sha(data) {
		t.Fatalf("the file found by its path differs: %v", err)
	}
	wantCode(t, l.run(Get, "--folder", l.folder, "/docs/2026/missing.bin", dest), 4)
	wantCode(t, l.run(Get, "/docs/2026/deep.bin", dest), 2)
}
