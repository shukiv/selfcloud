// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"bytes"
	"encoding/json"
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

// versionListPath is the route of a file's versions.
func versionListPath(nodeID string) string { return "/api/v1/nodes/" + nodeID + "/versions" }

// versionItemPath is the route of one version.
func versionItemPath(nodeID, verID string) string { return versionListPath(nodeID) + "/" + verID }

// versionContentPath is the route of one version's bytes.
func versionContentPath(nodeID, verID string) string {
	return versionItemPath(nodeID, verID) + "/content"
}

// versionRestorePath is the route that restores one version.
func versionRestorePath(nodeID, verID string) string {
	return versionItemPath(nodeID, verID) + "/restore"
}

// versionServer serves a node, its versions and a version's bytes, and records what it saw. It is the content route's
// server, so a download sees a strong ETag and Range, which the fake cannot answer.
type versionServer struct {
	t   *testing.T
	srv *httptest.Server

	nodeID   string
	name     string
	verID    string
	nodeETag string
	etag     string
	data     []byte
	list     []map[string]any
	// restoreErr, when set, is the answer to a restore instead of the node document.
	restoreErr reply

	mu      sync.Mutex
	calls   []string
	headers []http.Header
}

func newVersionServer(t *testing.T) *versionServer {
	t.Helper()
	data := []byte("the version's content, kept when the file changed")
	s := &versionServer{
		t:        t,
		nodeID:   id.Derive("nod", "cli11b-versions-node"),
		name:     "report.txt",
		verID:    id.Derive("ver", "cli11b-versions-one"),
		nodeETag: `"fedcba9876543210fedcba9876543210"`,
		etag:     fakeETag,
		data:     data,
	}
	s.list = []map[string]any{
		{"id": "current", "current": true, "size": len(data), "etag": s.nodeETag, "sha256": sha(data),
			"created_at": "2026-11-02T10:00:00Z", "label": nil, "keep_forever": false},
		{"id": s.verID, "current": false, "size": len(data), "mime_type": "text/plain", "etag": s.etag,
			"sha256": sha(data), "created_at": "2026-11-01T10:00:00Z", "created_by": "usr_author",
			"replaced_at": "2026-11-02T10:00:00Z", "replaced_by": "usr_editor", "label": "before the edit",
			"keep_forever": true},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *versionServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	s.headers = append(s.headers, r.Header.Clone())
	s.mu.Unlock()
	switch r.URL.Path {
	case "/api/v1/nodes/" + s.nodeID:
		doc := node(s.nodeID, s.name, "file")
		doc["size"] = len(s.data)
		doc["etag"] = s.nodeETag
		doc["sha256"] = sha(s.data)
		s.writeJSON(w, http.StatusOK, doc)
	case versionListPath(s.nodeID):
		s.writeJSON(w, http.StatusOK, map[string]any{"items": s.list})
	case versionContentPath(s.nodeID, s.verID):
		w.Header().Set("ETag", s.etag)
		http.ServeContent(w, r, s.name, time.Time{}, bytes.NewReader(s.data))
	case versionRestorePath(s.nodeID, s.verID):
		if s.restoreErr.status != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(s.restoreErr.status)
			_, _ = io.WriteString(w, s.restoreErr.body)
			return
		}
		s.writeJSON(w, http.StatusOK, node(s.nodeID, s.name, "file"))
	default:
		http.NotFound(w, r)
	}
}

func (s *versionServer) writeJSON(w http.ResponseWriter, status int, doc any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, jsonOf(doc))
}

func (s *versionServer) run(args ...string) result {
	s.t.Helper()
	var out, errOut bytes.Buffer
	code := VersionGet(append(append([]string{}, args...), "--url", s.srv.URL), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func (s *versionServer) runFn(fn func([]string, io.Writer, io.Writer) int, args ...string) result {
	s.t.Helper()
	var out, errOut bytes.Buffer
	code := fn(append(append([]string{}, args...), "--url", s.srv.URL), &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func (s *versionServer) saw() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *versionServer) header(i int, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headers[i].Get(key)
}

// versionLabelDoc is the version document a label change answers with.
func versionLabelDoc(verID string) map[string]any {
	return map[string]any{"id": verID, "current": false, "size": 34, "mime_type": "text/plain",
		"etag": fakeETag, "sha256": sha([]byte("0123456789012345678901234567890123")),
		"created_at": "2026-11-01T10:00:00Z", "created_by": "usr_author", "replaced_at": "2026-11-02T10:00:00Z",
		"replaced_by": "usr_editor", "label": "reviewed", "keep_forever": true}
}

// TestVersionLs prints the versions in the order the API gives them, the current one first. The REPLACED and LABEL
// columns are empty when the server gives no value, and KEEP says yes or nothing.
func TestVersionLs(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	n := id.Derive("nod", "cli11b-ls")
	ver := id.Derive("ver", "cli11b-ls-one")
	current := map[string]any{"id": "current", "current": true, "size": 12, "etag": fakeETag,
		"sha256": sha([]byte("0123456789ab")), "created_at": "2026-11-02T10:00:00Z", "label": nil,
		"keep_forever": false}
	old := map[string]any{"id": ver, "current": false, "size": 34, "mime_type": "text/plain",
		"etag": `"11112222333344445555666677778888"`, "sha256": sha([]byte("x")),
		"created_at": "2026-11-01T09:00:00Z", "created_by": "usr_author",
		"replaced_at": "2026-11-02T10:00:00Z", "replaced_by": "usr_editor", "label": "before", "keep_forever": true}
	f.on("GET", versionListPath(n), page("", current, old))

	res := f.run(VersionLs, n)
	wantCode(t, res, 0)
	f.wantCalls("GET " + versionListPath(n))
	want := table(
		[]string{"ID", "SIZE", "CREATED", "REPLACED", "LABEL", "KEEP"},
		[]string{"current", "12", "2026-11-02T10:00:00Z", "", "", ""},
		[]string{ver, "34", "2026-11-01T09:00:00Z", "2026-11-02T10:00:00Z", "before", "yes"},
	)
	if res.stdout != want {
		t.Fatalf("stdout %q, want %q", res.stdout, want)
	}
}

// TestVersionLsJSON prints exactly the version fields, so a field the view does not name does not reach the output.
func TestVersionLsJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	n := id.Derive("nod", "cli11b-ls-json")
	ver := id.Derive("ver", "cli11b-ls-json-one")
	current := map[string]any{"id": "current", "current": true, "size": 12, "etag": fakeETag,
		"created_at": "2026-11-02T10:00:00Z", "label": nil, "keep_forever": false}
	old := map[string]any{"id": ver, "current": false, "size": 34, "etag": fakeETag,
		"created_at": "2026-11-01T09:00:00Z", "label": "before", "keep_forever": true,
		"token": "sck_secret_value"}
	f.on("GET", versionListPath(n), page("", current, old))

	res := f.run(VersionLs, n, "--json")
	wantCode(t, res, 0)
	if strings.Contains(res.stdout, "sck_secret_value") {
		t.Fatalf("a field outside the view was printed: %q", res.stdout)
	}
	var views []map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &views); err != nil {
		t.Fatalf("stdout %q is not a JSON array: %v", res.stdout, err)
	}
	if len(views) != 2 {
		t.Fatalf("stdout %q holds %d views, want 2", res.stdout, len(views))
	}
	if views[0]["id"] != "current" || views[0]["label"] != nil || views[0]["keep_forever"] != false {
		t.Errorf("current view %v", views[0])
	}
	if views[1]["id"] != ver || views[1]["label"] != "before" || views[1]["keep_forever"] != true {
		t.Errorf("version view %v", views[1])
	}
	if _, ok := views[1]["token"]; ok {
		t.Errorf("version view kept an unknown field: %v", views[1])
	}
}

// TestVersionLsUsage: a node is one nod_ id, and the command sends nothing for anything else.
func TestVersionLsUsage(t *testing.T) {
	isolate(t, testAccountKey)
	for _, args := range [][]string{{}, {testNode, testOther}, {"not-a-node"}} {
		f := newFake(t)
		res := f.run(VersionLs, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

// TestVersionGet downloads a version into a directory: the name is the file's current name, so the node is read once,
// and the content request is a range with the version's ETag.
func TestVersionGet(t *testing.T) {
	isolate(t, testAccountKey)
	s := newVersionServer(t)
	dir := t.TempDir()

	res := s.runFn(VersionGet, s.nodeID, s.verID, dir)
	wantCode(t, res, 0)
	want := []string{"GET " + versionListPath(s.nodeID), "GET /api/v1/nodes/" + s.nodeID,
		"GET " + versionContentPath(s.nodeID, s.verID)}
	if got := s.saw(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %v, want %v", got, want)
	}
	if got := s.header(2, "Range"); got != "bytes=0-"+strconv.Itoa(len(s.data)-1) {
		t.Errorf("Range %q", got)
	}
	if got := s.header(2, "If-Range"); got != s.etag {
		t.Errorf("If-Range %q, want %q", got, s.etag)
	}
	if !strings.Contains(res.stdout, "path="+filepath.Join(dir, s.name)+"\n") {
		t.Errorf("stdout %q", res.stdout)
	}
	if !strings.Contains(res.stdout, "checked=sha256\n") {
		t.Errorf("stdout %q", res.stdout)
	}
	got, err := os.ReadFile(filepath.Join(dir, s.name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, s.data) {
		t.Errorf("the destination holds %q, want %q", got, s.data)
	}
}

// TestVersionGetNamedDest writes to the name the person chose, without reading the node.
func TestVersionGetNamedDest(t *testing.T) {
	isolate(t, testAccountKey)
	s := newVersionServer(t)
	dest := filepath.Join(t.TempDir(), "copy.bin")

	res := s.runFn(VersionGet, s.nodeID, s.verID, dest)
	wantCode(t, res, 0)
	want := []string{"GET " + versionListPath(s.nodeID), "GET " + versionContentPath(s.nodeID, s.verID)}
	if got := s.saw(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %v, want %v", got, want)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, s.data) {
		t.Errorf("the destination holds %q, want %q", got, s.data)
	}
}

// TestVersionGetResumes continues a part file this client began for the same version: the range starts at the part's
// size and carries the version's ETag in If-Range.
func TestVersionGetResumes(t *testing.T) {
	isolate(t, testAccountKey)
	s := newVersionServer(t)
	dir := t.TempDir()
	part := filepath.Join(dir, s.name+partSuffix)
	const done = 10
	if err := os.WriteFile(part, s.data[:done], 0o600); err != nil {
		t.Fatal(err)
	}
	err := saveDownloadRecord(downloadRecord{Part: part, Server: s.srv.URL, Node: s.nodeID, ETag: s.etag,
		Size: int64(len(s.data)), StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	res := s.runFn(VersionGet, s.nodeID, s.verID, dir)
	wantCode(t, res, 0)
	if got := s.header(2, "Range"); got != "bytes="+strconv.Itoa(done)+"-"+strconv.Itoa(len(s.data)-1) {
		t.Errorf("Range %q", got)
	}
	if got := s.header(2, "If-Range"); got != s.etag {
		t.Errorf("If-Range %q, want %q", got, s.etag)
	}
	if !strings.Contains(res.stderr, "going on from byte 10") {
		t.Errorf("stderr %q", res.stderr)
	}
	got, err := os.ReadFile(filepath.Join(dir, s.name))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, s.data) {
		t.Errorf("the destination holds %q, want %q", got, s.data)
	}
}

// TestVersionGetNoSuchVersion: a version the list does not hold is exit 4 after the one list request, and nothing is
// downloaded.
func TestVersionGetNoSuchVersion(t *testing.T) {
	isolate(t, testAccountKey)
	s := newVersionServer(t)
	missing := id.Derive("ver", "cli11b-not-in-the-list")

	res := s.runFn(VersionGet, s.nodeID, missing, t.TempDir())
	wantCode(t, res, 4)
	want := []string{"GET " + versionListPath(s.nodeID)}
	if got := s.saw(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %v, want %v", got, want)
	}
	if !strings.Contains(res.stderr, "no such version") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestVersionGetUsage: the ids are checked before any request, and current is not a version.
func TestVersionGetUsage(t *testing.T) {
	isolate(t, testAccountKey)
	for _, args := range [][]string{
		{},
		{testNode},
		{testNode, testOther, t.TempDir(), "extra"},
		{"not-a-node", id.Derive("ver", "cli11b-usage"), t.TempDir()},
		{testNode, "not-a-version", t.TempDir()},
		{testNode, "current", t.TempDir()},
	} {
		f := newFake(t)
		res := f.run(VersionGet, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

// TestVersionLabel sends only what was given, so an empty label removes it and --no-keep sends false.
func TestVersionLabel(t *testing.T) {
	isolate(t, testAccountKey)
	n := id.Derive("nod", "cli11b-label")
	ver := id.Derive("ver", "cli11b-label-one")
	cases := []struct {
		name string
		args []string
		body map[string]any
	}{
		{"label", []string{"--label", "reviewed"}, map[string]any{"label": "reviewed"}},
		{"empty label", []string{"--label", ""}, map[string]any{"label": ""}},
		{"keep", []string{"--keep"}, map[string]any{"keep_forever": true}},
		{"no keep", []string{"--no-keep"}, map[string]any{"keep_forever": false}},
		{"both", []string{"--label", "x", "--keep"}, map[string]any{"label": "x", "keep_forever": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake(t)
			doc := versionLabelDoc(ver)
			for k, v := range c.body {
				doc[k] = v
			}
			f.on("PATCH", versionItemPath(n, ver), ok(doc))
			res := f.run(VersionLabel, append([]string{n, ver}, c.args...)...)
			wantCode(t, res, 0)
			f.wantCalls("PATCH " + versionItemPath(n, ver))
			got := f.wantBody(0)
			if len(got) != len(c.body) {
				t.Fatalf("body %v, want %v", got, c.body)
			}
			for k, v := range c.body {
				if got[k] != v {
					t.Errorf("body[%q] = %v, want %v", k, got[k], v)
				}
			}
			for _, want := range []string{"id=" + ver + "\n", "label=" + labelOf(c.body) + "\n"} {
				if !strings.Contains(res.stdout, want) {
					t.Errorf("stdout %q does not hold %q", res.stdout, want)
				}
			}
		})
	}
}

// labelOf is the label the body sets, or the one the answer holds when the body does not change it.
func labelOf(body map[string]any) string {
	if v, ok := body["label"]; ok {
		return v.(string)
	}
	return "reviewed"
}

// TestVersionLabelJSON prints the view under --json.
func TestVersionLabelJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	n := id.Derive("nod", "cli11b-label-json")
	ver := id.Derive("ver", "cli11b-label-json-one")
	f.on("PATCH", versionItemPath(n, ver), ok(versionLabelDoc(ver)))

	res := f.run(VersionLabel, n, ver, "--label", "reviewed", "--json")
	wantCode(t, res, 0)
	var view map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &view); err != nil {
		t.Fatalf("stdout %q is not a JSON object: %v", res.stdout, err)
	}
	if view["id"] != ver || view["label"] != "reviewed" || view["keep_forever"] != true {
		t.Errorf("view %v", view)
	}
}

// TestVersionLabelUsage sends nothing for a bad id, no change, both keep flags, or a label over 100 characters.
func TestVersionLabelUsage(t *testing.T) {
	isolate(t, testAccountKey)
	n := id.Derive("nod", "cli11b-label-usage")
	ver := id.Derive("ver", "cli11b-label-usage-one")
	for _, args := range [][]string{
		{},
		{n},
		{n, ver},
		{n, ver, testOther, "extra"},
		{"not-a-node", ver, "--label", "x"},
		{n, "not-a-version", "--label", "x"},
		{n, "current", "--label", "x"},
		{n, ver, "--keep", "--no-keep"},
		{n, ver, "--label", strings.Repeat("é", 101)},
	} {
		f := newFake(t)
		res := f.run(VersionLabel, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

// TestVersionLabelConflict: the server's 409 is refused with its message.
func TestVersionLabelConflict(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	n := id.Derive("nod", "cli11b-label-conflict")
	ver := id.Derive("ver", "cli11b-label-conflict-one")
	f.on("PATCH", versionItemPath(n, ver), apiErr(409, "max_kept", "the file keeps no more versions"))

	res := f.run(VersionLabel, n, ver, "--keep")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "the file keeps no more versions") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestVersionRestore sends the node's own ETag in If-Match and prints the restored node.
func TestVersionRestore(t *testing.T) {
	isolate(t, testAccountKey)
	s := newVersionServer(t)

	res := s.runFn(VersionRestore, s.nodeID, s.verID)
	wantCode(t, res, 0)
	want := []string{"GET /api/v1/nodes/" + s.nodeID, "POST " + versionRestorePath(s.nodeID, s.verID)}
	if got := s.saw(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls %v, want %v", got, want)
	}
	if got := s.header(1, "If-Match"); got != s.nodeETag {
		t.Errorf("If-Match %q, want %q", got, s.nodeETag)
	}
	if !strings.Contains(res.stdout, "id="+s.nodeID+"\n") {
		t.Errorf("stdout %q", res.stdout)
	}
}

// TestVersionRestoreChanged: a file that changed since it was read is refused with the server's message.
func TestVersionRestoreChanged(t *testing.T) {
	isolate(t, testAccountKey)
	s := newVersionServer(t)
	s.restoreErr = apiErr(412, "precondition_failed", "the file changed since the etag was read")

	res := s.runFn(VersionRestore, s.nodeID, s.verID)
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "the file changed since the etag was read") {
		t.Errorf("stderr %q", res.stderr)
	}
}

// TestVersionRestoreUsage: the ids are checked before any request.
func TestVersionRestoreUsage(t *testing.T) {
	isolate(t, testAccountKey)
	for _, args := range [][]string{
		{},
		{testNode},
		{testNode, testOther, "extra"},
		{"not-a-node", id.Derive("ver", "cli11b-restore-usage")},
		{testNode, "not-a-version"},
		{testNode, "current"},
	} {
		f := newFake(t)
		res := f.run(VersionRestore, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

// TestVersionRmWithYes deletes the version and prints it.
func TestVersionRmWithYes(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	n := id.Derive("nod", "cli11b-rm")
	ver := id.Derive("ver", "cli11b-rm-one")
	f.on("DELETE", versionItemPath(n, ver), done(204))

	res := f.run(VersionRm, n, ver, "--yes")
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + versionItemPath(n, ver))
	if res.stdout != "deleted="+ver+"\n" {
		t.Fatalf("stdout %q", res.stdout)
	}
}

// TestVersionRmJSON prints the deletion under --json.
func TestVersionRmJSON(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	n := id.Derive("nod", "cli11b-rm-json")
	ver := id.Derive("ver", "cli11b-rm-json-one")
	f.on("DELETE", versionItemPath(n, ver), done(204))

	res := f.run(VersionRm, n, ver, "--yes", "--json")
	wantCode(t, res, 0)
	var got map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout %q is not a JSON object: %v", res.stdout, err)
	}
	if got["deleted"] != ver {
		t.Errorf("stdout %q", res.stdout)
	}
}

// TestVersionRmConfirms asks first, on the version's id: without a terminal it refuses before any request.
func TestVersionRmConfirms(t *testing.T) {
	isolate(t, testAccountKey)
	n := id.Derive("nod", "cli11b-rm-confirm")
	ver := id.Derive("ver", "cli11b-rm-confirm-one")

	f := newFake(t)
	res := f.run(VersionRm, n, ver)
	wantCode(t, res, 2)
	f.wantNoCalls()
	if !strings.Contains(res.stderr, "--yes") {
		t.Errorf("stderr %q", res.stderr)
	}

	f = newFake(t)
	typeAtTerminal("not-the-version\n")
	res = f.run(VersionRm, n, ver)
	wantCode(t, res, 2)
	f.wantNoCalls()

	f = newFake(t)
	f.on("DELETE", versionItemPath(n, ver), done(204))
	typeAtTerminal(ver + "\n")
	res = f.run(VersionRm, n, ver)
	wantCode(t, res, 0)
	f.wantCalls("DELETE " + versionItemPath(n, ver))
}

// TestVersionRmUsage: the ids are checked before the question and the request.
func TestVersionRmUsage(t *testing.T) {
	isolate(t, testAccountKey)
	for _, args := range [][]string{
		{},
		{testNode},
		{testNode, testOther, "extra"},
		{"not-a-node", id.Derive("ver", "cli11b-rm-usage")},
		{testNode, "not-a-version"},
		{testNode, "current"},
	} {
		f := newFake(t)
		res := f.run(VersionRm, args...)
		wantCode(t, res, 2)
		f.wantNoCalls()
	}
}

// TestVersionRmConflict: the server's refusal is exit 3 with its message.
func TestVersionRmConflict(t *testing.T) {
	isolate(t, testAccountKey)
	f := newFake(t)
	n := id.Derive("nod", "cli11b-rm-conflict")
	ver := id.Derive("ver", "cli11b-rm-conflict-one")
	f.on("DELETE", versionItemPath(n, ver), apiErr(409, "version_kept", "the version is kept forever"))

	res := f.run(VersionRm, n, ver, "--yes")
	wantCode(t, res, 3)
	if !strings.Contains(res.stderr, "the version is kept forever") {
		t.Errorf("stderr %q", res.stderr)
	}
}
