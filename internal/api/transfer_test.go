package api

import (
	"context"
	"strings"
	"testing"

	"selfcloud/internal/apierr"
	"selfcloud/internal/config"
	"selfcloud/internal/mail"
	"selfcloud/internal/store"
)

// bigTree makes a folder with 7 nodes under dir: 3 files, a subfolder with 2 files, and dir itself.
func bigTree(t *testing.T, s *Server, key, fld string) nodeJSON {
	t.Helper()
	dir := mkdirs(t, s, key, fld, "/tree")
	putNode(t, s, key, fld, "/tree/a.txt", "a")
	putNode(t, s, key, fld, "/tree/b.txt", "b")
	putNode(t, s, key, fld, "/tree/c.txt", "c")
	mkdirs(t, s, key, fld, "/tree/sub")
	putNode(t, s, key, fld, "/tree/sub/d.txt", "d")
	putNode(t, s, key, fld, "/tree/sub/e.txt", "e")
	return dir
}

// TestLargeCopyIsQueuedAndRuns checks that a copy above the sync threshold answers 202 with a job,
// and that the job makes the copy: the new root is the job's target, and a repeat does nothing.
func TestLargeCopyIsQueuedAndRuns(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	s.copySyncNodes = 3
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	tree := bigTree(t, s, key, f.ID)

	rec := call(t, s, "POST", "/api/v1/nodes/"+tree.ID+"/copy", key, map[string]any{"name": "copy"})
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)
	if job.Kind != "node.copy" || job.Target == "" || job.Status != "queued" {
		t.Fatalf("job = %+v", job)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/jobs/"+job.ID {
		t.Errorf("Location = %q", loc)
	}
	// Nothing is copied until the job runs.
	if n := len(decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children", key, nil)).Items); n != 1 {
		t.Fatalf("children before the job = %d, want 1", n)
	}
	if err := s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := decode[jobJSON](t, call(t, s, "GET", "/api/v1/jobs/"+job.ID, key, nil))
	if done.Status != "succeeded" {
		t.Fatalf("job = %+v", done)
	}
	root := decode[nodeJSON](t, call(t, s, "GET", "/api/v1/nodes/"+job.Target, key, nil))
	if root.Name != "copy" || root.Kind != "dir" {
		t.Fatalf("copied root = %+v", root)
	}
	kids := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/nodes/"+root.ID+"/children", key, nil))
	if len(kids.Items) != 4 {
		t.Errorf("copied children = %d, want 4", len(kids.Items))
	}
	// The usage counts the copies: 3 files at the top and two below, of one byte each.
	if u := usageOf(t, s, key, acc.ID); u.UsedFiles != 10 {
		t.Errorf("files after copy = %d, want 10", u.UsedFiles)
	}

	// Running it again, as after a restart, changes nothing.
	stored, err := s.store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.runCopyJob(context.Background(), stored); err != nil {
		t.Fatalf("repeat run = %v", err)
	}
	if u := usageOf(t, s, key, acc.ID); u.UsedFiles != 10 {
		t.Errorf("files after repeat = %d, want 10", u.UsedFiles)
	}
}

// TestQueuedCopyRechecksAccess checks that a copy queued by a member fails, and makes nothing, when the
// member's write grant on the target is gone by the time the job runs.
func TestQueuedCopyRechecksAccess(t *testing.T) {
	m := newMemberFixture(t)
	m.s.copySyncNodes = 2
	bigTree(t, m.s, m.key, m.f.ID)
	m.grant(t, m.g.ID, "write")

	rec := callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+treeID(t, m)+"/copy", map[string]any{"folder": m.g.ID})
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)
	m.grant(t, m.g.ID, "read")
	if err := m.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done := decode[jobJSON](t, call(t, m.s, "GET", "/api/v1/jobs/"+job.ID, m.key, nil)); done.Status != "failed" {
		t.Fatalf("job = %+v, want failed", done)
	}
	wantError(t, call(t, m.s, "GET", "/api/v1/nodes/"+job.Target, m.key, nil), 404, apierr.NotFound)
}

// treeID returns the ID of the "tree" folder made by bigTree.
func treeID(t *testing.T, m *memberFixture) string {
	t.Helper()
	list := decode[listResponse[nodeJSON]](t, call(t, m.s, "GET", "/api/v1/folders/"+m.f.ID+"/children?"+q("/"), m.key, nil))
	for _, n := range list.Items {
		if n.Name == "tree" {
			return n.ID
		}
	}
	t.Fatal("no tree")
	return ""
}

// TestCopyAboveMaxNodesIsRefused checks the cap, with its details.
func TestCopyAboveMaxNodesIsRefused(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Copy.MaxNodes = 5 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	tree := bigTree(t, s, key, f.ID)
	e := wantError(t, call(t, s, "POST", "/api/v1/nodes/"+tree.ID+"/copy", key, map[string]any{}), 413, apierr.TooLarge)
	if e.Details["max_nodes"] != float64(5) {
		t.Errorf("max_nodes = %v", e.Details["max_nodes"])
	}
	if n, _ := e.Details["nodes"].(float64); n < 6 {
		t.Errorf("nodes = %v, want at least 6", e.Details["nodes"])
	}
	if u := usageOf(t, s, key, acc.ID); u.UsedFiles != 5 {
		t.Errorf("a refused copy changed usage: %+v", u)
	}
}

// TestReadOnlyCopy checks that a copy needs read on the source and write on the target: read on both
// is refused, and read on the source with write on the target is allowed.
func TestReadOnlyCopy(t *testing.T) {
	m := newMemberFixture(t)
	// The member has write on f and read on g. Copying from g to f needs write on f: allowed; copying
	// from f to g needs write on g: refused.
	wantStatus(t, callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+m.fileG.ID+"/copy",
		map[string]any{"folder": m.f.ID, "on_conflict": "rename"}), 201)
	wantError(t, callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+m.fileF.ID+"/copy",
		map[string]any{"folder": m.g.ID}), 403, apierr.Forbidden)
	// Same folder with read only: the target is the source's folder, so write is needed there too.
	m.grant(t, m.f.ID, "read")
	wantError(t, callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+m.fileF.ID+"/copy",
		map[string]any{"on_conflict": "rename"}), 403, apierr.Forbidden)
}

// TestCaseOnlyMoveRename checks that a move which only changes a name's case renames the node.
func TestCaseOnlyMoveRename(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	a := putNode(t, s, key, f.ID, "/a.txt", "a")
	moved := decode[nodeJSON](t, call(t, s, "POST", "/api/v1/nodes/"+a.ID+"/move", key, map[string]any{"name": "A.txt"}))
	if moved.Name != "A.txt" {
		t.Errorf("case-only move rename = %q", moved.Name)
	}
}

// TestRenameLongNameStaysWithinLimit checks that "rename" on a name near the limit cuts the base and
// keeps the extension, so the new name is at most 255 bytes.
func TestRenameLongNameStaysWithinLimit(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	long := strings.Repeat("a", 250) + ".txt"
	putNode(t, s, key, f.ID, "/"+long, "x")
	cp := decode[nodeJSON](t, call(t, s, "POST", "/api/v1/nodes/"+nodeIn(t, s, key, f.ID, long)+"/copy", key,
		map[string]any{"on_conflict": "rename"}))
	if len(cp.Name) > 255 || !strings.HasSuffix(cp.Name, " (1).txt") {
		t.Errorf("renamed long name = %q (%d bytes)", cp.Name, len(cp.Name))
	}
}

// nodeIn returns the ID of the node called name in folder fld.
func nodeIn(t *testing.T, s *Server, key, fld, name string) string {
	t.Helper()
	list := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+fld+"/children", key, nil))
	for _, n := range list.Items {
		if n.Name == name {
			return n.ID
		}
	}
	t.Fatalf("no %q", name)
	return ""
}

// TestReplaceFileOverFile checks the replace policy for a move and a copy: the file is replaced and
// its contents are later collected; a folder on either side is a conflict.
func TestReplaceFileOverFile(t *testing.T) {
	m := newMemberFixture(t)
	m.grant(t, m.g.ID, "write")
	old := putNode(t, m.s, m.key, m.g.ID, "/doc.txt", "old contents")
	before := usageOf(t, m.s, m.key, m.acc.ID)

	moved := decode[nodeJSON](t, callSession(t, m.s, m.sess, "POST", "/api/v1/nodes/"+m.fileF.ID+"/move",
		map[string]any{"folder": m.g.ID, "on_conflict": "replace"}))
	if moved.Name != "doc.txt" || moved.ID != m.fileF.ID || moved.SHA256 != sha("v1") {
		t.Fatalf("moved = %+v", moved)
	}
	wantError(t, call(t, m.s, "GET", "/api/v1/nodes/"+old.ID, m.key, nil), 404, apierr.NotFound)
	after := usageOf(t, m.s, m.key, m.acc.ID)
	if before.UsedFiles-after.UsedFiles != 1 || before.UsedBytes-after.UsedBytes != int64(len("old contents")) {
		t.Errorf("usage before %+v, after %+v", before, after)
	}
	// The displaced contents are unreferenced now, so the collector removes them.
	if _, err := sweeper(m.s, 0).Once(context.Background(), store.Now()); err != nil {
		t.Fatal(err)
	}
	if n := blobFiles(t, m.s); n != 2 {
		t.Errorf("files after collection = %d, want 2 (the moved file and keep.txt)", n)
	}

	// A copy replaces too.
	putNode(t, m.s, m.key, m.g.ID, "/one.txt", "1")
	src := putNode(t, m.s, m.key, m.g.ID, "/two.txt", "2")
	rc := call(t, m.s, "POST", "/api/v1/nodes/"+src.ID+"/copy", m.key,
		map[string]any{"folder": m.g.ID, "path": "/", "name": "one.txt", "on_conflict": "replace"})
	wantStatus(t, rc, 201)
	copied := decode[nodeJSON](t, rc)
	if copied.Name != "one.txt" || copied.SHA256 != sha("2") {
		t.Errorf("copy over replace = %+v", copied)
	}
}

// TestReplaceRefusesFoldersAndSelf checks the refusals of replace: a folder on either side, and a
// copy onto itself.
func TestReplaceRefusesFoldersAndSelf(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	file := putNode(t, s, key, f.ID, "/x", "x")
	dir := mkdirs(t, s, key, f.ID, "/y")
	// A folder moved over a file, and a file moved over a folder.
	wantError(t, call(t, s, "POST", "/api/v1/nodes/"+dir.ID+"/move", key,
		map[string]any{"path": "/", "name": "x", "on_conflict": "replace"}), 409, apierr.Conflict)
	mkdirs(t, s, key, f.ID, "/z")
	fileOverDir := putNode(t, s, key, f.ID, "/w", "w")
	wantError(t, call(t, s, "POST", "/api/v1/nodes/"+fileOverDir.ID+"/move", key,
		map[string]any{"name": "z", "on_conflict": "replace"}), 409, apierr.Conflict)
	// A copy onto itself is refused, not replaced.
	wantError(t, call(t, s, "POST", "/api/v1/nodes/"+file.ID+"/copy", key,
		map[string]any{"on_conflict": "replace"}), 409, apierr.Conflict)
	wantStatus(t, call(t, s, "GET", "/api/v1/nodes/"+file.ID, key, nil), 200)
	// Unknown policies are invalid.
	wantError(t, call(t, s, "POST", "/api/v1/nodes/"+file.ID+"/copy", key,
		map[string]any{"on_conflict": "overwrite"}), 400, apierr.InvalidRequest)
}

// TestConflictPolicyAndDefaults keeps the default policy: a clash is a conflict.
func TestConflictPolicyAndDefaults(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	putNode(t, s, key, f.ID, "/a", "a")
	b := putNode(t, s, key, f.ID, "/b", "b")
	wantError(t, call(t, s, "POST", "/api/v1/nodes/"+b.ID+"/move", key, map[string]any{"name": "a"}), 409, apierr.Conflict)
}
