// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// onceBefore runs fn the first time a mutating request reaches the write lock: after its outside
// access check and body read, and before its transaction. Later requests are not affected.
func onceBefore(s *Server, fn func()) {
	fired := false
	s.beforeWrite = func() {
		if !fired {
			fired = true
			fn()
		}
	}
}

// memberFixture is an account with two folders, a member granted write on f and read on g, and
// a file in each. Keys make the setup changes; the member's session makes the requests under test.
type memberFixture struct {
	s     *Server
	acc   accountJSON
	key   string
	f, g  folderJSON
	ann   userResponse
	sess  testSession
	fileF nodeJSON // in f
	fileG nodeJSON // in g
}

func newMemberFixture(t *testing.T) *memberFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	m := &memberFixture{s: s, acc: acc, key: key}
	m.f = newFolder(t, s, key, acc.ID, "Mine")
	m.g = newFolder(t, s, key, acc.ID, "Other")
	m.fileF = putNode(t, s, key, m.f.ID, "/doc.txt", "v1")
	m.fileG = putNode(t, s, key, m.g.ID, "/keep.txt", "k")
	m.ann, m.sess = verifiedUser(t, s, key, acc.ID, "ann@example.com")
	m.grant(t, m.f.ID, "write")
	m.grant(t, m.g.ID, "read")
	return m
}

func (m *memberFixture) grant(t *testing.T, fld, level string) {
	t.Helper()
	wantStatus(t, call(t, m.s, "PUT", "/api/v1/accounts/"+m.acc.ID+"/folders/"+fld+"/grants/"+m.ann.ID, m.key,
		map[string]any{"level": level}), 200)
}

func (m *memberFixture) revoke(t *testing.T, fld string) {
	t.Helper()
	wantStatus(t, call(t, m.s, "DELETE", "/api/v1/accounts/"+m.acc.ID+"/folders/"+fld+"/grants/"+m.ann.ID, m.key, nil), 204)
}

// snapshot is what a failed write must leave unchanged: the usage and the file contents on disk.
func snapshot(t *testing.T, m *memberFixture) (usageJSON, int) {
	t.Helper()
	// The people counts change with a user's status, which these tests change on purpose; the snapshot
	// compares storage only.
	u := usageOf(t, m.s, m.key, m.acc.ID)
	u.UsersActive, u.UsersPending = 0, 0
	return u, blobFiles(t, m.s)
}

func TestPutRecheckedAtCommit(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	before, files := snapshot(t, m)
	onceBefore(m.s, func() { m.revoke(t, m.f.ID) })
	put := rawSession(t, m.s, m.sess, "PUT", "/api/v1/nodes/"+m.fileF.ID+"/content", "v2",
		map[string]string{"If-Match": m.fileF.ETag})
	wantError(t, put, 404, apierr.NotFound)
	if got := rawCall(t, m.s, "GET", "/api/v1/nodes/"+m.fileF.ID+"/content", m.key, "", nil).Body.String(); got != "v1" {
		t.Errorf("content after refused write = %q", got)
	}
	if after, n := snapshot(t, m); after != before || n != files {
		t.Errorf("refused write changed usage or files: %+v/%d -> %+v/%d", before, files, after, n)
	}
}

func TestCreateRecheckedAtCommit(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	before, files := snapshot(t, m)
	// The user is disabled between the check and the commit.
	onceBefore(m.s, func() {
		wantStatus(t, call(t, m.s, "PATCH", "/api/v1/accounts/"+m.acc.ID+"/users/"+m.ann.ID, m.key,
			map[string]any{"status": "disabled"}), 200)
	})
	wantError(t, rawSession(t, m.s, m.sess, "POST", "/api/v1/folders/"+m.f.ID+"/files?"+q("/new.txt"), "x", nil),
		403, apierr.UserDisabled)
	if after, n := snapshot(t, m); after != before || n != files {
		t.Errorf("refused create changed usage or files")
	}
	wantError(t, call(t, m.s, "GET", "/api/v1/folders/"+m.f.ID+"/children?"+q("/new.txt"), m.key, nil), 404, apierr.NotFound)
}

func TestMkdirRecheckedAtCommit(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	onceBefore(m.s, func() { m.grant(t, m.f.ID, "read") })
	wantError(t, callSession(t, m.s, m.sess, "POST", "/api/v1/folders/"+m.f.ID+"/directories",
		map[string]any{"path": "/d"}), 403, apierr.Forbidden)
	wantError(t, call(t, m.s, "GET", "/api/v1/folders/"+m.f.ID+"/children?"+q("/d"), m.key, nil), 404, apierr.NotFound)
}

func TestRenameRecheckedAtCommit(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	// Someone moves the file into a folder the member cannot write, between the check and the commit.
	onceBefore(m.s, func() {
		wantStatus(t, call(t, m.s, "POST", "/api/v1/nodes/"+m.fileF.ID+"/move", m.key,
			map[string]any{"folder": m.g.ID}), 200)
	})
	// The member reads the new folder but may not write it: forbidden, not not_found.
	wantError(t, callSession(t, m.s, m.sess, "PATCH", "/api/v1/nodes/"+m.fileF.ID, map[string]any{"name": "x.txt"}),
		403, apierr.Forbidden)
	if got := decode[nodeJSON](t, call(t, m.s, "GET", "/api/v1/nodes/"+m.fileF.ID, m.key, nil)); got.Name != "doc.txt" {
		t.Errorf("name after refused rename = %q", got.Name)
	}
}

func TestDeleteRecheckedAtCommit(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	onceBefore(m.s, func() { m.revoke(t, m.f.ID) })
	wantError(t, callSession(t, m.s, m.sess, "DELETE", "/api/v1/nodes/"+m.fileF.ID, nil), 404, apierr.NotFound)
	wantStatus(t, call(t, m.s, "GET", "/api/v1/nodes/"+m.fileF.ID, m.key, nil), 200)
}

func TestMoveRecheckedAtCommit(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	m.grant(t, m.g.ID, "write")
	before, files := snapshot(t, m)
	// The target's write grant drops to read between the check and the commit.
	onceBefore(m.s, func() { m.grant(t, m.g.ID, "read") })
	wantError(t, callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+m.fileF.ID+"/move",
		map[string]any{"folder": m.g.ID}), 403, apierr.Forbidden)
	if got := decode[nodeJSON](t, call(t, m.s, "GET", "/api/v1/nodes/"+m.fileF.ID, m.key, nil)); got.FolderID != m.f.ID {
		t.Errorf("refused move changed the folder to %s", got.FolderID)
	}
	if after, n := snapshot(t, m); after != before || n != files {
		t.Errorf("refused move changed usage or files")
	}
}

func TestCopyRecheckedAtCommit(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	m.grant(t, m.g.ID, "write")
	before, files := snapshot(t, m)
	// The source's read grant is revoked between the check and the commit.
	onceBefore(m.s, func() { m.revoke(t, m.f.ID) })
	wantError(t, callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+m.fileF.ID+"/copy",
		map[string]any{"folder": m.g.ID}), 404, apierr.NotFound)
	if after, n := snapshot(t, m); after != before || n != files {
		t.Errorf("refused copy changed usage or files")
	}
}

// TestCopyTargetFollowsMovedSource covers a copy that names no folder, so it copies within the
// source's folder. The source moves into a folder the member may only read between the check and the
// commit: the copy must be refused, not written into the read-only folder.
func TestCopyTargetFollowsMovedSource(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	before, files := snapshot(t, m)
	onceBefore(m.s, func() {
		wantStatus(t, call(t, m.s, "POST", "/api/v1/nodes/"+m.fileF.ID+"/move", m.key,
			map[string]any{"folder": m.g.ID}), 200)
	})
	wantError(t, callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+m.fileF.ID+"/copy",
		map[string]any{"name": "copy.txt"}), 403, apierr.Forbidden)
	for _, fld := range []string{m.f.ID, m.g.ID} {
		wantError(t, call(t, m.s, "GET", "/api/v1/folders/"+fld+"/children?"+q("/copy.txt"), m.key, nil),
			404, apierr.NotFound)
	}
	if after, n := snapshot(t, m); after != before || n != files {
		t.Errorf("refused copy changed usage or files")
	}
}

// TestDeadlineSetForBodyReads checks that a raw-body write sets a read deadline that grows with the
// size cap: 30 seconds plus a second per MiB, here 16 MiB.
func TestDeadlineSetForBodyReads(t *testing.T) {
	t.Parallel()
	m := newMemberFixture(t)
	req := httptest.NewRequest("POST", "/api/v1/folders/"+m.f.ID+"/files?"+q("/d.txt"), strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+m.key)
	dw := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	m.s.Handler().ServeHTTP(dw, req)
	wantStatus(t, dw.ResponseRecorder, 201)
	got := dw.deadline.Sub(start)
	if got < 45*time.Second || got > 47*time.Second {
		t.Errorf("read deadline = %v, want about 46s", got)
	}
}

// deadlineWriter is a response writer that accepts read deadlines, as the server's connections do.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (d *deadlineWriter) SetReadDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

var _ http.ResponseWriter = (*deadlineWriter)(nil)
