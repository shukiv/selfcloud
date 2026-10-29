// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
)

// rawCall sends a body that is not JSON, with extra headers and an optional bearer credential.
func rawCall(t *testing.T, s *Server, method, path, cred, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "text/plain")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// rawSession is rawCall with a session, sent as a browser would send it.
func rawSession(t *testing.T, s *Server, sess testSession, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	all := map[string]string{}
	for k, v := range hdr {
		all[k] = v
	}
	if method != http.MethodGet {
		all["Origin"] = s.origin
		all["X-CSRF-Token"] = sess.csrf
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	for k, v := range all {
		req.Header.Set(k, v)
	}
	req.AddCookie(sess.cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// q encodes a path for a ?path= query.
func q(p string) string { return url.Values{"path": {p}}.Encode() }

// putNode creates a file of the given contents and returns it.
func putNode(t *testing.T, s *Server, cred, fld, p, body string) nodeJSON {
	t.Helper()
	rec := rawCall(t, s, "POST", "/api/v1/folders/"+fld+"/files?"+q(p), cred, body, nil)
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

// mkdirs creates a directory path and returns the directory.
func mkdirs(t *testing.T, s *Server, cred, fld, p string) nodeJSON {
	t.Helper()
	rec := call(t, s, "POST", "/api/v1/folders/"+fld+"/directories", cred, map[string]any{"path": p, "parents": true})
	wantStatus(t, rec, 201)
	return decode[nodeJSON](t, rec)
}

func sha(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

func usageOf(t *testing.T, s *Server, key, accID string) usageJSON {
	t.Helper()
	return decode[usageJSON](t, call(t, s, "GET", "/api/v1/accounts/"+accID+"/usage", key, nil))
}

func TestParsePath(t *testing.T) {
	ok := map[string]int{"": 0, "/": 0, "a": 1, "/a/b.txt": 2, "a/b/c": 3}
	for p, n := range ok {
		segs, err := parsePath(p)
		if err != nil || len(segs) != n {
			t.Errorf("parsePath(%q) = %d segments, %v; want %d", p, len(segs), err, n)
		}
	}
	for _, p := range []string{"/a//b", "/a/", "/.", "/a/..", "/a\\b", "/a\x00b", "/a/​b",
		strings.Repeat("a/", maxPathLength)} {
		if _, err := parsePath(p); err == nil {
			t.Errorf("parsePath(%q) accepted", p)
		} else if code := apierr.From(err).Code; code != apierr.InvalidPath {
			t.Errorf("parsePath(%q) code %s, want invalid_path", p, code)
		}
	}
}

func TestListStatAndPaths(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")

	dir := mkdirs(t, s, key, f.ID, "/a/b")
	if dir.Kind != "dir" || dir.ParentID == nil {
		t.Fatalf("mkdir /a/b = %+v", dir)
	}
	file := putNode(t, s, key, f.ID, "/a/b/c.txt", "hello")
	if file.Size != 5 || file.MimeType != "text/plain" || file.SHA256 != sha("hello") ||
		!strings.HasPrefix(file.ETag, `"`) {
		t.Fatalf("file = %+v", file)
	}
	stat := decode[nodeJSON](t, call(t, s, "GET", "/api/v1/nodes/"+file.ID, key, nil))
	if stat.ETag != file.ETag || stat.SHA256 != sha("hello") {
		t.Errorf("stat = %+v", stat)
	}

	root := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children", key, nil))
	if len(root.Items) != 1 || root.Items[0].Name != "a" {
		t.Errorf("root children = %+v", root.Items)
	}
	inB := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children?"+q("/a/b"), key, nil))
	if len(inB.Items) != 1 || inB.Items[0].ID != file.ID {
		t.Errorf("children of /a/b = %+v", inB.Items)
	}
	byID := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/nodes/"+dir.ID+"/children", key, nil))
	if len(byID.Items) != 1 {
		t.Errorf("children by ID = %+v", byID.Items)
	}

	// A missing path is not_found; a path through a file is invalid; listing a file is refused.
	wantError(t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children?"+q("/a/zzz"), key, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children?"+q("/a/b/c.txt/x"), key, nil),
		400, apierr.InvalidPath)
	wantError(t, call(t, s, "GET", "/api/v1/nodes/"+file.ID+"/children", key, nil), 400, apierr.InvalidRequest)

	// Pages by name key: a, d1, d2, d3.
	for _, n := range []string{"d1", "d2", "d3"} {
		mkdirs(t, s, key, f.ID, "/"+n)
	}
	page := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children?limit=2", key, nil))
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	rest := decode[listResponse[nodeJSON]](t, call(t, s, "GET",
		"/api/v1/folders/"+f.ID+"/children?limit=2&cursor="+url.QueryEscape(page.NextCursor), key, nil))
	if len(rest.Items) != 2 || rest.NextCursor != "" || rest.Items[1].Name != "d3" {
		t.Errorf("second page = %+v", rest)
	}
	wantError(t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children?limit=0", key, nil), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children?limit=2&cursor=bad", key, nil), 400, apierr.InvalidRequest)
}

func TestMkdirRules(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	mkdir := "/api/v1/folders/" + f.ID + "/directories"

	// Without parents, a missing parent is not_found.
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/x/y"}), 404, apierr.NotFound)
	wantStatus(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/x"}), 201)
	// An existing directory is a conflict, unless parents makes mkdir -p of it, which is 200.
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/x"}), 409, apierr.Conflict)
	wantStatus(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/x", "parents": true}), 200)
	// A file in the way is a conflict as the last name, and an invalid path in the middle.
	putNode(t, s, key, f.ID, "/x/f.txt", "1")
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/x/f.txt", "parents": true}),
		409, apierr.Conflict)
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/x/f.txt/z", "parents": true}),
		400, apierr.InvalidPath)
	// The root cannot be made, and a bad name is refused with its own code.
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/"}), 400, apierr.InvalidPath)
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/a/../b"}), 400, apierr.InvalidPath)
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/X"}), 409, apierr.Conflict)
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/bad\\name"}), 400, apierr.InvalidPath)
	wantError(t, call(t, s, "POST", mkdir, key, map[string]any{"path": "/a", "extra": 1}), 400, apierr.InvalidRequest)
}

func TestCreateFileRules(t *testing.T) {
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec, func(c *config.Config) { c.Upload.MaxSmallFileSize = 1024 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	putNode(t, s, key, f.ID, "/one.txt", "1")

	// A name that exists: a conflict, or a precondition failure with If-None-Match: *.
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/one.txt"), key, "2", nil),
		409, apierr.Conflict)
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/one.txt"), key, "2",
		map[string]string{"If-None-Match": "*"}), 412, apierr.PreconditionFailed)
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/two.txt"), key, "2",
		map[string]string{"If-None-Match": `"abc"`}), 400, apierr.InvalidRequest)

	// A missing directory is not_found unless parents is set.
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/no/such.txt"), key, "2", nil),
		404, apierr.NotFound)
	wantStatus(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/no/such.txt")+"&parents=true",
		key, "2", nil), 201)
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/x.txt")+"&parents=maybe", key, "2", nil),
		400, apierr.InvalidRequest)
	wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/"), key, "2", nil),
		400, apierr.InvalidPath)

	// Over the small-file cap: refused before the body is written, with the limit.
	big := strings.Repeat("x", 2048)
	e := wantError(t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/big.bin"), key, big, nil),
		413, apierr.TooLarge)
	if e.Details["limit_bytes"] != float64(1024) {
		t.Errorf("limit_bytes = %v", e.Details["limit_bytes"])
	}
	// A type the client did not send comes from the extension.
	png := decode[nodeJSON](t, rawCall(t, s, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/pic.png"), key, "x",
		map[string]string{"Content-Type": "application/octet-stream"}))
	if png.MimeType != "image/png" {
		t.Errorf("mime from extension = %q", png.MimeType)
	}
	if got := len(rec.Messages()); got != 0 {
		t.Errorf("mail sent %d", got)
	}
}

func TestRenameMoveCopyDelete(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	one := newFolder(t, s, key, acc.ID, "One")
	two := newFolder(t, s, key, acc.ID, "Two")
	src := mkdirs(t, s, key, one.ID, "/src")
	a := putNode(t, s, key, one.ID, "/src/a.txt", "aaa")
	sub := mkdirs(t, s, key, one.ID, "/src/sub")
	b := putNode(t, s, key, one.ID, "/src/sub/b.txt", "bb")
	node := "/api/v1/nodes/"

	// Rename: a sibling's name is a conflict; the same name in another case is this node's own name.
	wantStatus(t, call(t, s, "PATCH", node+a.ID, key, map[string]any{"name": "Renamed.txt"}), 200)
	wantError(t, call(t, s, "PATCH", node+a.ID, key, map[string]any{"name": "SUB"}), 409, apierr.Conflict)
	wantStatus(t, call(t, s, "PATCH", node+a.ID, key, map[string]any{"name": "renamed.txt"}), 200)

	// Moves: a folder cannot go into itself or below itself.
	wantError(t, call(t, s, "POST", node+src.ID+"/move", key, map[string]any{"parent": src.ID}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", node+src.ID+"/move", key, map[string]any{"parent": sub.ID}), 400, apierr.InvalidRequest)
	// A parent in another folder, or an unknown one, is not_found; a file is not a parent.
	wantError(t, call(t, s, "POST", node+a.ID+"/move", key, map[string]any{"folder": two.ID, "parent": sub.ID}),
		404, apierr.NotFound)
	wantError(t, call(t, s, "POST", node+a.ID+"/move", key, map[string]any{"parent": id.New("nod")}), 404, apierr.NotFound)
	wantError(t, call(t, s, "POST", node+a.ID+"/move", key, map[string]any{"parent": b.ID}), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "POST", node+a.ID+"/move", key, map[string]any{"parent": sub.ID, "path": "/"}),
		400, apierr.InvalidRequest)
	// Moving within the folder by path.
	moved := decode[nodeJSON](t, call(t, s, "POST", node+sub.ID+"/move", key, map[string]any{"path": "/"}))
	if moved.ParentID == nil || *moved.ParentID == src.ID {
		t.Errorf("move to root = %+v", moved)
	}

	// A move to another folder rewrites the folder of the whole subtree.
	dirMoved := decode[nodeJSON](t, call(t, s, "POST", node+src.ID+"/move", key,
		map[string]any{"folder": two.ID}))
	if dirMoved.FolderID != two.ID {
		t.Fatalf("folder after move = %s", dirMoved.FolderID)
	}
	inTwo := decode[nodeJSON](t, call(t, s, "GET", node+a.ID, key, nil))
	if inTwo.FolderID != two.ID {
		t.Errorf("descendant stayed in %s", inTwo.FolderID)
	}
	// On a name clash the move fails, or, with on_conflict rename, takes a new name.
	putNode(t, s, key, two.ID, "/dup.txt", "d")
	dup := putNode(t, s, key, one.ID, "/dup.txt", "e")
	wantError(t, call(t, s, "POST", node+dup.ID+"/move", key, map[string]any{"folder": two.ID}), 409, apierr.Conflict)
	renamed := decode[nodeJSON](t, call(t, s, "POST", node+dup.ID+"/move", key,
		map[string]any{"folder": two.ID, "on_conflict": "rename"}))
	if renamed.Name != "dup (1).txt" {
		t.Errorf("renamed on conflict = %q", renamed.Name)
	}
	// on_conflict is only read when there is a clash: a second "dup.txt" in One clashes with Two's.
	putNode(t, s, key, one.ID, "/dup.txt", "f")
	dup2 := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+one.ID+"/children?"+q("/"), key, nil))
	var second nodeJSON
	for _, n := range dup2.Items {
		if n.Name == "dup.txt" {
			second = n
		}
	}
	wantError(t, call(t, s, "POST", node+second.ID+"/move", key,
		map[string]any{"folder": two.ID, "on_conflict": "bogus"}), 400, apierr.InvalidRequest)

	// Copies share the blob: the same hash, a new node, and usage counts the copy in full.
	before := usageOf(t, s, key, acc.ID)
	// A copy into the original's own folder clashes with it, unless on_conflict says rename.
	wantError(t, call(t, s, "POST", node+a.ID+"/copy", key, map[string]any{}), 409, apierr.Conflict)
	cp := decode[nodeJSON](t, call(t, s, "POST", node+a.ID+"/copy", key, map[string]any{"on_conflict": "rename"}))
	if cp.ID == a.ID || cp.SHA256 != sha("aaa") || cp.ETag == a.ETag || cp.Name != "renamed (1).txt" {
		t.Fatalf("copy = %+v", cp)
	}
	cp2 := decode[nodeJSON](t, call(t, s, "POST", node+a.ID+"/copy", key, map[string]any{"on_conflict": "rename"}))
	if cp2.Name != "renamed (2).txt" {
		t.Errorf("second copy name = %q", cp2.Name)
	}
	after := usageOf(t, s, key, acc.ID)
	if after.UsedBytes-before.UsedBytes != 2*3 || after.UsedFiles-before.UsedFiles != 2 {
		t.Errorf("usage after two copies: %+v -> %+v", before, after)
	}
	// Copying a folder copies its files and folders; copying it into itself is refused.
	tree := mkdirs(t, s, key, one.ID, "/tree")
	putNode(t, s, key, one.ID, "/tree/t1.txt", "1")
	mkdirs(t, s, key, one.ID, "/tree/inner")
	putNode(t, s, key, one.ID, "/tree/inner/t2.txt", "22")
	wantError(t, call(t, s, "POST", node+tree.ID+"/copy", key, map[string]any{"parent": tree.ID}),
		400, apierr.InvalidRequest)
	copied := decode[nodeJSON](t, call(t, s, "POST", node+tree.ID+"/copy", key, map[string]any{"name": "tree copy"}))
	if copied.Kind != "dir" || copied.Name != "tree copy" {
		t.Fatalf("copied folder = %+v", copied)
	}
	inCopy := decode[listResponse[nodeJSON]](t, call(t, s, "GET", node+copied.ID+"/children", key, nil))
	if len(inCopy.Items) != 2 {
		t.Errorf("copied children = %+v", inCopy.Items)
	}

	// Delete: a folder takes its files off the usage; the root cannot be deleted.
	beforeDel := usageOf(t, s, key, acc.ID)
	wantStatus(t, call(t, s, "DELETE", node+tree.ID+"?permanent=true", key, nil), 204)
	afterDel := usageOf(t, s, key, acc.ID)
	if beforeDel.UsedFiles-afterDel.UsedFiles != 2 || beforeDel.UsedBytes-afterDel.UsedBytes != 3 {
		t.Errorf("usage after deleting tree: %+v -> %+v", beforeDel, afterDel)
	}
	wantError(t, call(t, s, "DELETE", node+tree.ID, key, nil), 404, apierr.NotFound)
}

func TestContentReadAndWrite(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Upload.MaxSmallFileSize = 1024 })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	doc := putNode(t, s, key, f.ID, "/doc.txt", "v1")
	nodes := "/api/v1/nodes/" + doc.ID + "/content"

	get := rawCall(t, s, "GET", nodes, key, "", nil)
	wantStatus(t, get, 200)
	if get.Body.String() != "v1" || get.Header().Get("ETag") != doc.ETag ||
		get.Header().Get("X-Content-Type-Options") != "nosniff" ||
		get.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" ||
		!strings.HasPrefix(get.Header().Get("Content-Disposition"), "attachment") ||
		get.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("download: %q %v", get.Body.String(), get.Header())
	}
	wantStatus(t, rawCall(t, s, "GET", nodes, key, "", map[string]string{"If-None-Match": doc.ETag}), 304)

	// Writes need a current If-Match; a missing or stale one is refused.
	wantError(t, rawCall(t, s, "PUT", nodes, key, "v2", nil), 428, apierr.PreconditionRequired)
	wantError(t, rawCall(t, s, "PUT", nodes, key, "v2", map[string]string{"If-Match": "*"}), 400, apierr.InvalidRequest)
	wantError(t, rawCall(t, s, "PUT", nodes, key, "v2", map[string]string{"If-Match": `"stale"`}),
		412, apierr.PreconditionFailed)

	before := usageOf(t, s, key, acc.ID)
	put := rawCall(t, s, "PUT", nodes, key, "version two", map[string]string{"If-Match": doc.ETag})
	wantStatus(t, put, 200)
	updated := decode[nodeJSON](t, put)
	if updated.ETag == doc.ETag || updated.SHA256 != sha("version two") || updated.Size != 11 {
		t.Fatalf("updated = %+v", updated)
	}
	if got := rawCall(t, s, "GET", nodes, key, "", nil).Body.String(); got != "version two" {
		t.Errorf("content after write = %q", got)
	}
	// The old version's ETag no longer matches.
	wantError(t, rawCall(t, s, "PUT", nodes, key, "x", map[string]string{"If-Match": doc.ETag}),
		412, apierr.PreconditionFailed)
	after := usageOf(t, s, key, acc.ID)
	// The new content counts in full, and the old content is kept as a version, which counts too.
	if after.UsedBytes-before.UsedBytes != 11 || after.UsedFiles != before.UsedFiles {
		t.Errorf("usage after write: %+v -> %+v", before, after)
	}

	big := strings.Repeat("y", 2048)
	wantError(t, rawCall(t, s, "PUT", nodes, key, big, map[string]string{"If-Match": updated.ETag}), 413, apierr.TooLarge)

	// A folder has no contents.
	dir := mkdirs(t, s, key, f.ID, "/d")
	wantError(t, rawCall(t, s, "GET", "/api/v1/nodes/"+dir.ID+"/content", key, "", nil), 400, apierr.InvalidRequest)
}

func TestNodeAuthorization(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	other, otherKey := newAccount(t, s, admin, "Other")
	f := newFolder(t, s, key, acc.ID, "Docs")
	g := newFolder(t, s, key, acc.ID, "Shared")
	r := putNode(t, s, key, f.ID, "/r.txt", "r")
	shared := putNode(t, s, key, g.ID, "/s.txt", "s")
	foreign := putNode(t, s, otherKey, otherFolder(t, s, otherKey, other.ID), "/o.txt", "o")
	ann, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	grant := func(fld, level string) {
		t.Helper()
		wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+fld+"/grants/"+ann.ID, key,
			map[string]any{"level": level}), 200)
	}
	grant(f.ID, "read")
	grant(g.ID, "read")
	node := "/api/v1/nodes/"

	// A member with read sees the folder and cannot change anything in it.
	wantStatus(t, callSession(t, s, annSess, "GET", node+r.ID, nil), 200)
	wantStatus(t, callSession(t, s, annSess, "GET", "/api/v1/folders/"+f.ID+"/children", nil), 200)
	wantStatus(t, rawSession(t, s, annSess, "GET", node+r.ID+"/content", "", nil), 200)
	wantError(t, callSession(t, s, annSess, "POST", "/api/v1/folders/"+f.ID+"/directories",
		map[string]any{"path": "/x"}), 403, apierr.Forbidden)
	wantError(t, rawSession(t, s, annSess, "POST", "/api/v1/folders/"+f.ID+"/files?"+q("/x.txt"), "x", nil),
		403, apierr.Forbidden)
	wantError(t, callSession(t, s, annSess, "PATCH", node+r.ID, map[string]any{"name": "z"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, annSess, "DELETE", node+r.ID, nil), 403, apierr.Forbidden)
	wantError(t, rawSession(t, s, annSess, "PUT", node+r.ID+"/content", "x",
		map[string]string{"If-Match": r.ETag}), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, annSess, "POST", node+r.ID+"/copy", map[string]any{}), 403, apierr.Forbidden)

	// Write on the source only: a move to a folder the member reads is refused.
	grant(f.ID, "write")
	wantError(t, callSession(t, s, annSess, "POST", node+r.ID+"/move", map[string]any{"folder": g.ID}),
		403, apierr.Forbidden)
	// Read on the source, write on the target: refused too.
	grant(f.ID, "read")
	grant(g.ID, "write")
	wantError(t, callSession(t, s, annSess, "POST", node+shared.ID+"/copy", map[string]any{"folder": f.ID}),
		403, apierr.Forbidden)
	// Write on both: allowed.
	grant(f.ID, "write")
	wantStatus(t, callSession(t, s, annSess, "POST", node+r.ID+"/move", map[string]any{"folder": g.ID}), 200)
	wantStatus(t, callSession(t, s, annSess, "PATCH", node+r.ID, map[string]any{"name": "renamed.txt"}), 200)

	// Another account's nodes and folders are not found, whatever the caller holds.
	wantError(t, call(t, s, "GET", node+foreign.ID, key, nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "DELETE", node+foreign.ID, key, nil), 404, apierr.NotFound)
	wantError(t, rawCall(t, s, "GET", node+foreign.ID+"/content", key, "", nil), 404, apierr.NotFound)
	wantError(t, rawCall(t, s, "GET", node+"nod_nothere/content", key, "", nil), 404, apierr.NotFound)
	wantError(t, call(t, s, "POST", node+shared.ID+"/move", otherKey, map[string]any{"folder": g.ID}),
		404, apierr.NotFound)
	wantError(t, call(t, s, "POST", "/api/v1/folders/"+g.ID+"/directories", otherKey, map[string]any{"path": "/q"}),
		404, apierr.NotFound)
	wantError(t, call(t, s, "POST", node+shared.ID+"/move", key, map[string]any{"folder": foreignFolderID(t, s, otherKey, other.ID)}),
		404, apierr.NotFound)

	// Admins are refused on every node and folder route, with their key or a session.
	_, adminSess := newAdminUser(t, s, admin, "root@example.com")
	wantError(t, call(t, s, "GET", node+shared.ID, admin, nil), 403, apierr.Forbidden)
	wantError(t, call(t, s, "PATCH", node+shared.ID, admin, map[string]any{"name": "x"}), 403, apierr.Forbidden)
	wantError(t, callSession(t, s, adminSess, "GET", "/api/v1/folders/"+g.ID+"/children", nil), 403, apierr.Forbidden)
	wantError(t, call(t, s, "POST", "/api/v1/folders/"+g.ID+"/directories", admin, map[string]any{"path": "/q"}),
		403, apierr.Forbidden)
}

// otherFolder makes a folder in another account and returns its ID.
func otherFolder(t *testing.T, s *Server, key, accID string) string {
	t.Helper()
	return newFolder(t, s, key, accID, "Theirs").ID
}

// foreignFolderID makes a folder in another account, for a move target that must not be found.
func foreignFolderID(t *testing.T, s *Server, key, accID string) string {
	t.Helper()
	return newFolder(t, s, key, accID, "Foreign").ID
}

func TestMemberFoldersArePaged(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, annSess := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	for _, name := range []string{"One", "Two", "Three"} {
		fld := newFolder(t, s, key, acc.ID, name)
		wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+fld.ID+"/grants/"+ann.ID, key,
			map[string]any{"level": "read"}), 200)
	}
	first := decode[listResponse[folderAccessJSON]](t, callSession(t, s, annSess, "GET", "/api/v1/folders?limit=2", nil))
	if len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].Kind != "personal" {
		t.Fatalf("first page = %+v", first)
	}
	second := decode[listResponse[folderAccessJSON]](t, callSession(t, s, annSess, "GET",
		"/api/v1/folders?limit=2&cursor="+url.QueryEscape(first.NextCursor), nil))
	// The personal folder takes the first slot, so the three granted folders make 4 with it.
	if len(second.Items) != 2 || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
	// Folders made in the same millisecond are ordered by ID, so only the set is checked.
	seen := map[string]bool{}
	for _, f := range append(first.Items, second.Items...) {
		seen[f.Name] = true
	}
	if len(seen) != 4 {
		t.Errorf("paged folders cover %v, want the three granted and the personal folder", seen)
	}
}

// TestNodesAreAudited checks that every change to the tree leaves an audit entry.
func TestNodesAreAudited(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	d := mkdirs(t, s, key, f.ID, "/d")
	a := putNode(t, s, key, f.ID, "/a.txt", "a")
	wantStatus(t, call(t, s, "PATCH", "/api/v1/nodes/"+a.ID, key, map[string]any{"name": "b.txt"}), 200)
	wantStatus(t, call(t, s, "POST", "/api/v1/nodes/"+a.ID+"/move", key, map[string]any{"parent": d.ID}), 200)
	wantStatus(t, call(t, s, "POST", "/api/v1/nodes/"+a.ID+"/copy", key, map[string]any{"on_conflict": "rename"}), 201)
	// Renames and moves are writes, so the ETag read at creation is stale: read it again.
	current := decode[nodeJSON](t, call(t, s, "GET", "/api/v1/nodes/"+a.ID, key, nil))
	wantStatus(t, rawCall(t, s, "PUT", "/api/v1/nodes/"+a.ID+"/content", key, "new",
		map[string]string{"If-Match": current.ETag}), 200)
	wantStatus(t, call(t, s, "DELETE", "/api/v1/nodes/"+a.ID, key, nil), 204)

	log := decode[listResponse[auditJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", key, nil))
	seen := map[string]bool{}
	for _, e := range log.Items {
		seen[e.Action] = true
	}
	for _, want := range []string{"node.create", "node.rename", "node.move", "node.copy", "node.write", "node.trash"} {
		if !seen[want] {
			t.Errorf("no %s in the audit log", want)
		}
	}
}
