// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
)

// noWait makes the transfers repeat at once after a failure, for the length of the test.
func noWait(t *testing.T) {
	t.Helper()
	old := transferWait
	t.Cleanup(func() { transferWait = old })
	transferWait = func(int) time.Duration { return 0 }
}

// writeLocal writes data to a new file called name in a temporary directory and returns its path.
func writeLocal(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var uploadIDRE = regexp.MustCompile(`upl_[0-9a-z]{26}`)

// uploadIDIn returns the upload id a command's output names.
func uploadIDIn(t *testing.T, text string) string {
	t.Helper()
	m := uploadIDRE.FindString(text)
	if m == "" {
		t.Fatalf("no upload id in %q", text)
	}
	return m
}

// wantLive checks the exit code, and that the output carries neither the live server's key nor the fake credentials.
func (l *live) wantCode(res result, want int) {
	l.t.Helper()
	wantCode(l.t, res, want)
	if strings.Contains(res.stdout+res.stderr, l.key) {
		l.t.Fatalf("the account key was printed: stdout %q, stderr %q", res.stdout, res.stderr)
	}
}

// uploadsDir is where the upload records are kept, under the profiles directory isolate made.
func uploadsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("SELFCLOUD_PROFILES_DIR"), "transfers", "uploads")
}

// records lists the upload record files.
func records(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(uploadsDir(t))
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

// patches returns the pieces the record saw.
func patches(calls []seen) []seen {
	var out []seen
	for _, c := range calls {
		if c.method == http.MethodPatch {
			out = append(out, c)
		}
	}
	return out
}

// count returns how many calls had method and path, with an id segment written as its prefix in braces.
func countCalls(calls []seen, route string) int {
	n := 0
	for _, c := range calls {
		if routeOfCall(c.method+" "+c.path) == route {
			n++
		}
	}
	return n
}

var idSeg = regexp.MustCompile(`^([a-z]+)_[0-9a-z]{26}$`)

// routeOfCall writes each id segment of a call's path as its prefix in braces.
func routeOfCall(call string) string {
	method, path, _ := strings.Cut(call, " ")
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if m := idSeg.FindStringSubmatch(s); m != nil {
			segs[i] = "{" + m[1] + "}"
		}
	}
	return method + " " + strings.Join(segs, "/")
}

// TestPutSendsAFileInCheckedPieces: put creates one upload, sends the file in pieces of --piece-size, each with the
// SHA-256 of its bytes, prints the new node, and forgets the upload once it is a file.
func TestPutSendsAFileInCheckedPieces(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	data := randomBytes(t, 3*mib+mib/2)
	local := writeLocal(t, "pieces.bin", data)
	res := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	nodeID := valueOfLine(t, res.stdout, "id=")
	if got := l.content(nodeID); sha(got) != sha(data) {
		t.Fatalf("the file on the server differs from the local one")
	}
	if v := l.stat(nodeID); v.Name != "pieces.bin" || v.Size != int64(len(data)) {
		t.Errorf("node = %+v", v)
	}
	calls := l.rec.all()
	if n := countCalls(calls, "POST /api/v1/uploads"); n != 1 {
		t.Errorf("%d creations, want 1", n)
	}
	ps := patches(calls)
	wantLens := []int64{mib, mib, mib, mib / 2}
	if len(ps) != len(wantLens) {
		t.Fatalf("%d pieces, want %d", len(ps), len(wantLens))
	}
	for i, p := range ps {
		if p.length != wantLens[i] || p.offset != strconv.FormatInt(int64(i)*mib, 10) {
			t.Errorf("piece %d: %d bytes at %s", i, p.length, p.offset)
		}
		piece := data[int64(i)*mib : int64(i)*mib+p.length]
		if want := "sha256 " + sha256b64(piece); p.checksum != want {
			t.Errorf("piece %d: Upload-Checksum %q, want %q", i, p.checksum, want)
		}
	}
	if r := records(t); len(r) != 0 {
		t.Errorf("records left after the upload finished: %v", r)
	}
}

// TestPutResumesFromTheServersOffsetAfterThreeCuts is the fault-injection proof for uploads (decision 142): a
// 5 MiB file in 1 MiB pieces arrives whole after three cut connections. After each cut, put asks the server how far it
// got (HEAD) and goes on from that offset: it never starts a new upload, and never sends from zero again.
func TestPutResumesFromTheServersOffsetAfterThreeCuts(t *testing.T) {
	cut := newCutter(false, -1, 3*mib/2, 5*mib/4, 13*mib/5)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	data := randomBytes(t, 5*mib)
	local := writeLocal(t, "cut.bin", data)
	res := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	l.wantCode(res, 0)
	if cut.count() != 3 {
		t.Fatalf("%d cuts, want 3", cut.count())
	}
	nodeID := valueOfLine(t, res.stdout, "id=")
	if got := l.content(nodeID); sha(got) != sha(data) {
		t.Fatalf("the file on the server differs from the local one after the cuts")
	}
	calls := l.rec.all()
	if n := countCalls(calls, "POST /api/v1/uploads"); n != 1 {
		t.Errorf("%d creations, want 1: a cut must not start the upload again", n)
	}
	heads := 0
	for i, c := range calls {
		if c.method != http.MethodHead {
			continue
		}
		heads++
		if c.answerOffset == "" || c.answerOffset == "0" {
			t.Errorf("HEAD %d answered offset %q; the cuts all come after the first piece", heads, c.answerOffset)
		}
		next := -1
		for j := i + 1; j < len(calls); j++ {
			if calls[j].method == http.MethodPatch {
				next = j
				break
			}
		}
		if next < 0 {
			t.Fatalf("no piece after HEAD %d", heads)
		}
		if calls[next].offset != c.answerOffset {
			t.Errorf("after HEAD %d (offset %s) the next piece went at %s, not at the server's offset", heads,
				c.answerOffset, calls[next].offset)
		}
	}
	if heads < 3 {
		t.Errorf("%d HEADs, want one after each of the 3 cuts", heads)
	}
	for _, p := range patches(calls)[1:] {
		if p.offset == "0" {
			t.Errorf("a piece was sent from zero again after the first")
		}
	}
}

// expiryRun sends one piece of a 5 MiB file in 1 MiB pieces, then loses the server (every later connection is closed at
// once), so put gives up and keeps the upload. After wait, it resumes on the same server, now reachable.
func expiryRun(t *testing.T, mutate func(*config.Config), wait time.Duration) (l *live, data []byte, before int, resumed result) {
	t.Helper()
	cut := newCutter(false, 0, 3*mib/2)
	l = newLive(t, mutate, cut)
	l.isolate()
	noWait(t)
	data = randomBytes(t, 5*mib)
	local := writeLocal(t, "expiring.bin", data)
	first := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	l.wantCode(first, 1)
	upl := uploadIDIn(t, first.stderr)
	if !strings.Contains(first.stderr, "--resume "+upl) {
		t.Errorf("the failure does not say how to resume: %q", first.stderr)
	}
	before = len(patches(l.rec.all()))
	time.Sleep(wait)
	cut.setAfter(-1)
	resumed = l.run(Put, "--resume", upl, "--piece-size", "1MiB")
	return l, data, before, resumed
}

// wantExpired checks the expiry answer: exit 6, upload_expired, the file named and the instruction to start again, and
// no piece sent after the server said so.
func wantExpired(t *testing.T, l *live, name string, before int, resumed result) {
	t.Helper()
	l.wantCode(resumed, 6)
	if !strings.Contains(resumed.stderr, "upload_expired") || !strings.Contains(resumed.stderr, name) ||
		!strings.Contains(resumed.stderr, "start") {
		t.Errorf("stderr %q does not name the expiry, the file and the restart", resumed.stderr)
	}
	calls := l.rec.all()
	if n := len(patches(calls)); n != before {
		t.Errorf("%d pieces sent after the expiry, want none", n-before)
	}
	if n := countCalls(calls, "POST /api/v1/uploads"); n != 1 {
		t.Errorf("%d creations: an expired upload must not be started again silently", n)
	}
	// The expiry is never retried: once the server answers 410, nothing more is asked of the upload.
	expiredAt := -1
	for i, c := range calls {
		if c.status == http.StatusGone {
			expiredAt = i
			break
		}
	}
	if expiredAt < 0 {
		t.Fatal("the server never answered 410 Gone")
	}
	for _, c := range calls[expiredAt+1:] {
		if strings.HasPrefix(c.path, "/api/v1/uploads") {
			t.Errorf("%s %s was sent after the upload expired", c.method, c.path)
		}
	}
	if r := records(t); len(r) != 0 {
		t.Errorf("the expired upload is still recorded: %v", r)
	}
}

// TestPutPastTheUploadsLifetimeExits6 is the expiry proof of decision 142 for upload.max_lifetime: the server's own
// limit, set to two seconds in its configuration, ends the upload; the resume exits 6. The same run with the limit at its
// default finishes, with a matching SHA-256.
func TestPutPastTheUploadsLifetimeExits6(t *testing.T) {
	short := func(c *config.Config) { c.Upload.MaxLifetime = config.Duration(2 * time.Second) }
	t.Run("expired", func(t *testing.T) {
		l, _, before, resumed := expiryRun(t, short, 2500*time.Millisecond)
		wantExpired(t, l, "expiring.bin", before, resumed)
	})
	t.Run("lifted", func(t *testing.T) {
		l, data, _, resumed := expiryRun(t, nil, 2500*time.Millisecond)
		l.wantCode(resumed, 0)
		if got := l.content(valueOfLine(t, resumed.stdout, "id=")); sha(got) != sha(data) {
			t.Fatal("the resumed upload differs from the local file")
		}
	})
}

// TestPutIdleUploadExits6 is the same proof for upload.expiry, the idle limit: one second here.
// TestPutExpiryDuringTheUploadExits6: an upload that expires while put is still sending it ends that run at once with
// exit 6. The wait after a cut connection outlasts the server's upload.max_lifetime; the HEAD that follows answers 410,
// and nothing more is sent. With the limit at its default, the same run finishes.
func TestPutExpiryDuringTheUploadExits6(t *testing.T) {
	run := func(t *testing.T, mutate func(*config.Config)) (*live, []byte, result) {
		cut := newCutter(false, -1, 3*mib/2)
		l := newLive(t, mutate, cut)
		l.isolate()
		old := transferWait
		t.Cleanup(func() { transferWait = old })
		transferWait = func(try int) time.Duration {
			if try == 1 {
				return 2500 * time.Millisecond
			}
			return 0
		}
		data := randomBytes(t, 3*mib)
		local := writeLocal(t, "running.bin", data)
		return l, data, l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	}
	t.Run("expired", func(t *testing.T) {
		l, _, res := run(t, func(c *config.Config) { c.Upload.MaxLifetime = config.Duration(2 * time.Second) })
		wantExpired(t, l, "running.bin", len(patches(l.rec.all())), res)
	})
	t.Run("lifted", func(t *testing.T) {
		l, data, res := run(t, nil)
		l.wantCode(res, 0)
		if got := l.content(valueOfLine(t, res.stdout, "id=")); sha(got) != sha(data) {
			t.Fatal("the upload differs from the local file")
		}
	})
}

func TestPutIdleUploadExits6(t *testing.T) {
	idle := func(c *config.Config) { c.Upload.Expiry = config.Duration(time.Second) }
	t.Run("expired", func(t *testing.T) {
		l, _, before, resumed := expiryRun(t, idle, 1500*time.Millisecond)
		wantExpired(t, l, "expiring.bin", before, resumed)
	})
	t.Run("lifted", func(t *testing.T) {
		l, data, _, resumed := expiryRun(t, nil, 1500*time.Millisecond)
		l.wantCode(resumed, 0)
		if got := l.content(valueOfLine(t, resumed.stdout, "id=")); sha(got) != sha(data) {
			t.Fatal("the resumed upload differs from the local file")
		}
	})
}

// TestPutPieceSizeLimits: the piece is at least 1 MiB and never above the server's max_chunk_size. A size given by flag
// or by SELFCLOUD_PIECE_SIZE that breaks a limit is refused before the upload is created; the default takes the
// server's limit when it is lower.
func TestPutPieceSizeLimits(t *testing.T) {
	l := newLive(t, func(c *config.Config) { c.Upload.MaxChunkSize = 2 * mib }, nil)
	l.isolate()
	data := randomBytes(t, 5*mib)
	local := writeLocal(t, "sizes.bin", data)

	for _, bad := range []string{"512KiB", "0", "lots", "-1MiB"} {
		res := l.run(Put, local, "--folder", l.folder, "--piece-size", bad)
		l.wantCode(res, 2)
	}
	t.Setenv("SELFCLOUD_PIECE_SIZE", "100KiB")
	l.wantCode(l.run(Put, local, "--folder", l.folder), 2)
	if n := countCalls(l.rec.all(), "POST /api/v1/uploads"); n != 0 {
		t.Fatalf("%d uploads created for refused piece sizes", n)
	}

	t.Setenv("SELFCLOUD_PIECE_SIZE", "4MiB")
	res := l.run(Put, local, "--folder", l.folder)
	l.wantCode(res, 2)
	if !strings.Contains(res.stderr, strconv.Itoa(2*mib)) {
		t.Errorf("the refusal does not name the server's limit: %q", res.stderr)
	}
	if n := countCalls(l.rec.all(), "POST /api/v1/uploads"); n != 0 {
		t.Fatalf("an upload was created for a piece above the server's limit")
	}

	// The flag wins over the environment.
	res = l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB", "--name", "flag.bin")
	l.wantCode(res, 0)
	for _, p := range patches(l.rec.all()) {
		if p.length > mib {
			t.Errorf("a piece of %d bytes; --piece-size 1MiB wins over the environment", p.length)
		}
	}

	t.Setenv("SELFCLOUD_PIECE_SIZE", "")
	before := len(patches(l.rec.all()))
	res = l.run(Put, local, "--folder", l.folder, "--name", "default.bin")
	l.wantCode(res, 0)
	ps := patches(l.rec.all())[before:]
	if len(ps) != 3 || ps[0].length != 2*mib {
		t.Errorf("the default piece against a 2 MiB limit: %d pieces, the first %d bytes", len(ps), ps[0].length)
	}
}

// TestPutNameTakenDuringTheUploadWaitsForACommit: when another file takes the name while the upload runs, the last
// piece leaves the upload waiting for a name. put exits 3 and names the upload; put --resume with --name commits it.
func TestPutNameTakenDuringTheUploadWaitsForACommit(t *testing.T) {
	cut := newCutter(false, 0, 3*mib/2)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	data := randomBytes(t, 3*mib)
	local := writeLocal(t, "taken.bin", data)
	first := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	l.wantCode(first, 1)
	upl := uploadIDIn(t, first.stderr)
	l.upload("/", "taken.bin", []byte("someone else's"), "fail")
	cut.setAfter(-1)

	waiting := l.run(Put, "--resume", upl, "--piece-size", "1MiB")
	l.wantCode(waiting, 3)
	if !strings.Contains(waiting.stderr, upl) || !strings.Contains(waiting.stderr, "--name") {
		t.Errorf("the conflict does not say how to commit the upload: %q", waiting.stderr)
	}

	done := l.run(Put, "--resume", upl, "--name", "taken (mine).bin")
	l.wantCode(done, 0)
	nodeID := valueOfLine(t, done.stdout, "id=")
	if v := l.stat(nodeID); v.Name != "taken (mine).bin" {
		t.Errorf("committed as %q", v.Name)
	}
	if got := l.content(nodeID); sha(got) != sha(data) {
		t.Error("the committed file differs from the local one")
	}
	if n := countCalls(l.rec.all(), "POST /api/v1/uploads/{upl}/commit"); n != 1 {
		t.Errorf("%d commits, want 1", n)
	}
	if r := records(t); len(r) != 0 {
		t.Errorf("records left after the commit: %v", r)
	}
}

// TestPutResumeRefusesAChangedLocalFile: a resume sends nothing when the local file is not the one the upload began with.
func TestPutResumeRefusesAChangedLocalFile(t *testing.T) {
	for _, tc := range []struct {
		what string
		size int64
	}{{"another size", 3*mib + 1}, {"the same size, written again", 3 * mib}} {
		t.Run(tc.what, func(t *testing.T) {
			cut := newCutter(false, 0, 3*mib/2)
			l := newLive(t, nil, cut)
			l.isolate()
			noWait(t)
			local := writeLocal(t, "changing.bin", randomBytes(t, 3*mib))
			first := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
			l.wantCode(first, 1)
			upl := uploadIDIn(t, first.stderr)
			if err := os.WriteFile(local, randomBytes(t, int(tc.size)), 0o600); err != nil {
				t.Fatal(err)
			}
			later := time.Now().Add(time.Minute)
			if err := os.Chtimes(local, later, later); err != nil {
				t.Fatal(err)
			}
			cut.setAfter(-1)
			before := len(l.rec.all())
			res := l.run(Put, "--resume", upl)
			l.wantCode(res, 2)
			if got := l.rec.all()[before:]; len(patches(got)) != 0 {
				t.Errorf("pieces sent from a changed file: %d", len(patches(got)))
			}
		})
	}
}

// TestPutStopsWhenTheLocalFileChangesDuringTheUpload: the local file is checked before each piece, so a file written
// again while put runs, with its old size and a new time, stops the upload (exit 1, local_changed) before the next
// piece, rather than send pieces of two versions.
func TestPutStopsWhenTheLocalFileChangesDuringTheUpload(t *testing.T) {
	cut := newCutter(false, -1, 3*mib/2)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	local := writeLocal(t, "moving.bin", randomBytes(t, 3*mib))
	rewrite := randomBytes(t, 3*mib)
	var once sync.Once
	var hookErr error
	cut.onCut = func(int) {
		once.Do(func() {
			if hookErr = os.WriteFile(local, rewrite, 0o600); hookErr == nil {
				later := time.Now().Add(time.Minute)
				hookErr = os.Chtimes(local, later, later)
			}
		})
	}
	res := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	l.wantCode(res, 1)
	if !strings.Contains(res.stderr, "local_changed") {
		t.Errorf("stderr %q does not say the local file changed", res.stderr)
	}
	if n := len(patches(l.rec.all())); n != 2 {
		t.Errorf("%d pieces sent, want 2: the one taken and the one cut", n)
	}
}

// TestPutResumeRefusesAnotherServer: an upload is resumed only on the server it was started on.
func TestPutResumeRefusesAnotherServer(t *testing.T) {
	cut := newCutter(false, 0, 3*mib/2)
	l := newLive(t, nil, cut)
	l.isolate()
	noWait(t)
	local := writeLocal(t, "where.bin", randomBytes(t, 3*mib))
	first := l.run(Put, local, "--folder", l.folder, "--piece-size", "1MiB")
	l.wantCode(first, 1)
	upl := uploadIDIn(t, first.stderr)
	other := newFake(t)
	res := other.run(Put, "--resume", upl)
	wantCode(t, res, 2)
	other.wantNoCalls()
}

// TestPutEmptyFile: a file of no bytes is complete at its creation.
func TestPutEmptyFile(t *testing.T) {
	l := newLive(t, nil, nil)
	l.isolate()
	local := writeLocal(t, "empty.txt", nil)
	res := l.run(Put, local, "--folder", l.folder)
	l.wantCode(res, 0)
	if v := l.stat(valueOfLine(t, res.stdout, "id=")); v.Size != 0 || v.Name != "empty.txt" {
		t.Errorf("node = %+v", v)
	}
	if n := len(patches(l.rec.all())); n != 0 {
		t.Errorf("%d pieces for an empty file", n)
	}
}

// TestPutRefusesWhatIsNotAFile: a directory, a missing file and a bad remote name are refused before any call.
func TestPutRefusesWhatIsNotAFile(t *testing.T) {
	f := newFake(t)
	isolate(t, testAccountKey)
	dir := t.TempDir()
	wantCode(t, f.run(Put, dir, "--folder", testFolder), 2)
	wantCode(t, f.run(Put, filepath.Join(dir, "missing"), "--folder", testFolder), 2)
	local := writeLocal(t, "ok.bin", []byte("x"))
	wantCode(t, f.run(Put, local, "--folder", testFolder, "--name", "a/b"), 2)
	wantCode(t, f.run(Put, local), 2)
	wantCode(t, f.run(Put, local, "/x", "--folder", "nope"), 2)
	wantCode(t, f.run(Put, local, "--folder", testFolder, "--replace", "--rename"), 2)
	wantCode(t, f.run(Put, "--resume", "upl_nope"), 2)
	f.wantNoCalls()
}

// TestPutUsesTheUploadIDNotTheLocationsHost: the creation's Location may name any host. put takes only the upload id
// from it, and sends every piece to its own server, so the key never goes to the host the answer names.
func TestPutUsesTheUploadIDNotTheLocationsHost(t *testing.T) {
	isolate(t, testAccountKey)
	var mu sync.Mutex
	elsewhere := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhere++
		mu.Unlock()
	}))
	defer other.Close()
	upl := id.Derive("upl", "cli6-location")
	nodeID := id.Derive("nod", "cli6-location")
	var patched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/info":
			_, _ = w.Write([]byte(`{"limits":{"max_file_size":1073741824,"max_chunk_size":67108864}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/uploads":
			w.Header().Set("Location", other.URL+"/api/v1/uploads/"+upl)
			w.Header().Set("Upload-Offset", "0")
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch:
			mu.Lock()
			patched = append(patched, r.URL.Path)
			mu.Unlock()
			w.Header().Set("Upload-Offset", "5")
			w.Header().Set("Selfcloud-Node-ID", nodeID)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/nodes/"+nodeID:
			_, _ = w.Write([]byte(jsonOf(node(nodeID, "hello.txt", "file"))))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	local := writeLocal(t, "hello.txt", []byte("hello"))
	var out, errOut strings.Builder
	code := Put([]string{local, "--folder", testFolder, "--url", srv.URL}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if elsewhere != 0 {
		t.Errorf("the Location's host was called %d time(s)", elsewhere)
	}
	if len(patched) != 1 || patched[0] != "/api/v1/uploads/"+upl {
		t.Errorf("pieces went to %v", patched)
	}
}

// valueOfLine returns the rest of the first line of out that starts with key.
func valueOfLine(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, key); ok {
			return v
		}
	}
	t.Fatalf("no %s line in %q", key, out)
	return ""
}

// sha256b64 is the standard base64 of the SHA-256 of b, as Upload-Checksum carries it.
func sha256b64(b []byte) string {
	s := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(s[:])
}
