// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// sharingFixture is an account with a team space that holds /a/b/file.txt and /a/c/x.txt, and two
// members, ann and bob. The account key makes the setup changes; a member's session makes the
// requests under test.
type sharingFixture struct {
	s       *Server
	acc     accountJSON
	key     string
	admin   string
	fld     folderJSON
	a, b, c nodeJSON
	deep    nodeJSON // /a/b/file.txt
	sib     nodeJSON // /a/c/x.txt
	ann     userResponse
	annSess testSession
	bob     userResponse
	bobSess testSession
}

func newSharingFixture(t *testing.T) *sharingFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fx := &sharingFixture{s: s, acc: acc, key: key, admin: admin}
	fx.fld = newFolder(t, s, key, acc.ID, "Team")
	fx.a = mkdirs(t, s, key, fx.fld.ID, "/a")
	fx.b = mkdirs(t, s, key, fx.fld.ID, "/a/b")
	fx.c = mkdirs(t, s, key, fx.fld.ID, "/a/c")
	fx.deep = putNode(t, s, key, fx.fld.ID, "/a/b/file.txt", "deep")
	fx.sib = putNode(t, s, key, fx.fld.ID, "/a/c/x.txt", "sib")
	fx.ann, fx.annSess = verifiedUser(t, s, key, acc.ID, "ann@example.com")
	fx.bob, fx.bobSess = verifiedUser(t, s, key, acc.ID, "bob@example.com")
	return fx
}

// setPerm gives a subject a role on a node, as the account key.
func (fx *sharingFixture) setPerm(t *testing.T, nodeID, subject, role string) {
	t.Helper()
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+nodeID+"/permissions/"+subject, fx.key,
		map[string]any{"role": role}), 200)
}

// rootOf returns the root node of a folder, read from the store.
func (fx *sharingFixture) rootOf(t *testing.T, folderID string) string {
	t.Helper()
	root, err := fx.s.store.RootNode(context.Background(), fx.acc.ID, folderID)
	if err != nil {
		t.Fatal(err)
	}
	return root.ID
}

// stat is a member's GET of a node.
func (fx *sharingFixture) stat(t *testing.T, sess testSession, nodeID string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+nodeID, nil)
}

func (fx *sharingFixture) group(t *testing.T, name string) string {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/groups", fx.key, map[string]any{"name": name})
	wantStatus(t, rec, 201)
	return decode[groupJSON](t, rec).ID
}

func (fx *sharingFixture) inGroup(t *testing.T, grp, usr string, add bool) {
	t.Helper()
	method := "PUT"
	want := 204
	if !add {
		method, want = "DELETE", 204
	}
	wantStatus(t, call(t, fx.s, method, "/api/v1/accounts/"+fx.acc.ID+"/groups/"+grp+"/members/"+usr, fx.key, nil), want)
}

func (fx *sharingFixture) personalFolder(t *testing.T, sess testSession) string {
	t.Helper()
	me := decode[meResponse](t, callSession(t, fx.s, sess, "GET", "/api/v1/me", nil))
	if me.PersonalFolderID == nil {
		t.Fatal("the user has no personal folder")
	}
	return *me.PersonalFolderID
}

func TestPermissionOnDirectoryReachesDeepFile(t *testing.T) {
	fx := newSharingFixture(t)
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)

	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)
	if got := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)); got.Role != "viewer" {
		t.Errorf("role on the deep file = %q, want viewer", got.Role)
	}
	// A sibling directory stays closed, and so do the directories above the granted one.
	wantError(t, fx.stat(t, fx.annSess, fx.sib.ID), 404, apierr.NotFound)
	wantError(t, fx.stat(t, fx.annSess, fx.a.ID), 404, apierr.NotFound)
	// The folder's root stays not_found for a user whose only permission is deep.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", childrenPath(fx.fld.ID), nil), 404, apierr.NotFound)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", childrenPath(fx.fld.ID)+"?"+q("/a/b"), nil), 200)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", childrenPath(fx.fld.ID)+"?"+q("/a"), nil), 404, apierr.NotFound)
	// A user's shared folder lists its children with the role of each.
	kids := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.b.ID+"/children", nil))
	if len(kids.Items) != 1 || kids.Items[0].ID != fx.deep.ID || kids.Items[0].Role != "viewer" {
		t.Errorf("children of the shared directory = %+v", kids.Items)
	}
}

func TestGroupPermissionFollowsMembership(t *testing.T) {
	fx := newSharingFixture(t)
	grp := fx.group(t, "Staff")
	fx.inGroup(t, grp, fx.ann.ID, true)
	fx.setPerm(t, fx.b.ID, grp, "editor")
	if got := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)); got.Role != "editor" {
		t.Errorf("role through the group = %q, want editor", got.Role)
	}

	// Leaving the group revokes it at once; rejoining restores it.
	fx.inGroup(t, grp, fx.ann.ID, false)
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	fx.inGroup(t, grp, fx.ann.ID, true)
	wantStatus(t, fx.stat(t, fx.annSess, fx.deep.ID), 200)

	// Deleting the group revokes it for everyone, and takes its permission rows with it.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/groups/"+grp, fx.key, nil), 204)
	wantError(t, fx.stat(t, fx.annSess, fx.deep.ID), 404, apierr.NotFound)
	perms := decode[listResponse[permissionJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/permissions", fx.key, nil))
	if len(perms.Items) != 0 {
		t.Errorf("permissions left after the group was deleted = %+v", perms.Items)
	}
}

func TestEffectiveRoleIsTheMax(t *testing.T) {
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	// A viewer at the root and an editor below gives editor below and viewer elsewhere.
	if got := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)); got.Role != "editor" {
		t.Errorf("deep role = %q, want editor", got.Role)
	}
	if got := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.sib.ID)); got.Role != "viewer" {
		t.Errorf("sibling role = %q, want viewer", got.Role)
	}

	// A deeper viewer, through a group, never lowers the editor a user holds above it.
	grp := fx.group(t, "Readers")
	fx.inGroup(t, grp, fx.ann.ID, true)
	fx.setPerm(t, fx.b.ID, grp, "viewer")
	if got := decode[nodeJSON](t, fx.stat(t, fx.annSess, fx.deep.ID)); got.Role != "editor" {
		t.Errorf("deep role after a deeper viewer = %q, want editor", got.Role)
	}
	// An account key holds manager everywhere, and says so in the node JSON.
	if got := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil)); got.Role != "manager" {
		t.Errorf("key role = %q, want manager", got.Role)
	}
}

func TestRoleGatesPerOperation(t *testing.T) {
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "contributor")
	fld := fx.fld.ID

	// A contributor creates and uploads inside the directory, and replaces a file's content.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fld+"/directories",
		map[string]any{"path": "/a/b/new"}), 201)
	wantStatus(t, rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fld+"/files?"+q("/a/b/up.txt"),
		"up", nil), 201)
	wantStatus(t, rawSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/content", "new",
		map[string]string{"If-Match": fx.deep.ETag}), 200)
	// Rename and delete need editor.
	wantError(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/nodes/"+fx.deep.ID, map[string]any{"name": "z"}),
		403, apierr.Forbidden)
	wantError(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/nodes/"+fx.deep.ID, nil), 403, apierr.Forbidden)

	// A viewer writes nothing.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fld+"/directories",
		map[string]any{"path": "/a/b/v"}), 403, apierr.Forbidden)
	wantError(t, rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fld+"/files?"+q("/a/b/v.txt"), "v", nil),
		403, apierr.Forbidden)
	wantError(t, rawSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+fx.deep.ID+"/content", "x",
		map[string]string{"If-Match": "\"nope\""}), 403, apierr.Forbidden)
	// Copying needs contributor on the target. Reaching the target as a viewer is not enough.
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy",
		map[string]any{"parent": fx.c.ID}), 403, apierr.Forbidden)
}

func TestRevokedBetweenCheckAndCommit(t *testing.T) {
	t.Run("move", func(t *testing.T) {
		fx := newSharingFixture(t)
		fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
		fx.setPerm(t, fx.c.ID, fx.ann.ID, "contributor")
		// The target's permission goes between the early check and the commit.
		onceBefore(fx.s, func() {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.c.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
		})
		wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/move",
			map[string]any{"parent": fx.c.ID}), 404, apierr.NotFound)
		got := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil))
		if got.ParentID == nil || *got.ParentID != fx.b.ID {
			t.Errorf("refused move changed the parent")
		}
	})

	t.Run("copy job", func(t *testing.T) {
		fx := newSharingFixture(t)
		fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
		fx.setPerm(t, fx.c.ID, fx.ann.ID, "contributor")
		fx.s.copySyncNodes = 0 // every copy becomes a job
		rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy",
			map[string]any{"parent": fx.c.ID})
		wantStatus(t, rec, 202)
		job, err := fx.s.store.Job(context.Background(), path.Base(rec.Header().Get("Location")))
		if err != nil {
			t.Fatal(err)
		}
		// The contributor role on the target goes before the job runs.
		fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
		_ = fx.s.jobs.RunPending(context.Background())
		if j, err := fx.s.store.Job(context.Background(), job.ID); err != nil || j.Status != store.JobFailed {
			t.Errorf("copy job after the revoke = %+v, %v; want it failed", j, err)
		}
		if kids := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", fx.key, nil)); len(kids.Items) != 1 {
			t.Errorf("target holds %d entries after a refused copy, want 1", len(kids.Items))
		}
	})

	t.Run("upload completion", func(t *testing.T) {
		fx := newSharingFixture(t)
		fx.setPerm(t, fx.c.ID, fx.ann.ID, "contributor")
		rec := upSession(t, fx.s, fx.annSess, "POST", "/api/v1/uploads", map[string]string{
			"Upload-Length":   "3",
			"Upload-Metadata": md("folder", fx.fld.ID, "parent", fx.c.ID, "name", "new.txt"),
		}, nil, 0)
		wantStatus(t, rec, 201)
		upl := uploadID(t, rec)
		// The last piece completes the upload. The target's permission goes before the completion.
		onceBefore(fx.s, func() {
			wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.c.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)
		})
		wantError(t, upSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/uploads/"+upl, map[string]string{
			"Upload-Offset": "0", "Content-Type": "application/offset+octet-stream",
		}, strings.NewReader("abc"), 3), 404, apierr.NotFound)
		if kids := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", fx.key, nil)); len(kids.Items) != 1 {
			t.Errorf("target holds %d entries after a refused completion, want 1", len(kids.Items))
		}
	})
}

func TestOwnerManagesTeamSpacesNotPersonalFolders(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
		map[string]any{"role": "owner"}), 200)
	// An owner manages every team space, so it may set permissions there.
	if got := decode[nodeJSON](t, fx.stat(t, fx.bobSess, fx.deep.ID)); got.Role != "manager" {
		t.Errorf("owner role on a team space = %q, want manager", got.Role)
	}
	wantStatus(t, callSession(t, fx.s, fx.bobSess, "PUT", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID,
		map[string]any{"role": "viewer"}), 200)

	// Another user's personal folder is closed to the owner.
	annRoot := fx.rootOf(t, fx.personalFolder(t, fx.annSess))
	wantError(t, fx.stat(t, fx.bobSess, annRoot), 404, apierr.NotFound)
	// The owner's list has the team spaces and its own personal folder, not someone else's.
	list := decode[listResponse[folderAccessJSON]](t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/folders", nil))
	seen := map[string]bool{}
	for _, f := range list.Items {
		seen[f.ID] = true
	}
	if !seen[fx.fld.ID] || seen[fx.personalFolder(t, fx.annSess)] {
		t.Errorf("owner's folders = %+v", list.Items)
	}
}

func TestPersonalFolderPrivateButShareable(t *testing.T) {
	fx := newSharingFixture(t)
	pf := fx.personalFolder(t, fx.annSess)
	root := fx.rootOf(t, pf)
	// The user is manager of its own folder: it makes a directory and shares it.
	dir := decode[nodeJSON](t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+pf+"/directories",
		map[string]any{"path": "/shared", "parents": true}))
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/nodes/"+dir.ID+"/permissions/"+fx.bob.ID,
		map[string]any{"role": "viewer"}), 200)

	// The account key reaches the personal folder; a user it was not shared with does not.
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/nodes/"+root, fx.key, nil), 200)
	wantError(t, fx.stat(t, fx.bobSess, root), 404, apierr.NotFound)
	wantStatus(t, fx.stat(t, fx.bobSess, dir.ID), 200)

	// The key lists the personal folder with its kind; a team space is listed as one too.
	keyList := decode[listResponse[folderJSON]](t, call(t, fx.s, "GET", "/api/v1/folders", fx.key, nil))
	kinds := map[string]string{}
	for _, f := range keyList.Items {
		kinds[f.ID] = f.Kind
	}
	if kinds[pf] != "personal" || kinds[fx.fld.ID] != "team" {
		t.Errorf("kinds seen by the key = %v", kinds)
	}
}

func TestSharedListsOnlyWhatWasShared(t *testing.T) {
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	shared := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/shared", nil))
	if len(shared.Items) != 1 || shared.Items[0].ID != fx.b.ID || shared.Items[0].Role != "viewer" {
		t.Fatalf("shared with bob = %+v, want the directory b at viewer", shared.Items)
	}
	// Nothing above the shared directory is reached: not its parent, not the root.
	wantError(t, fx.stat(t, fx.bobSess, fx.a.ID), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "GET", childrenPath(fx.fld.ID), nil), 404, apierr.NotFound)

	// Once the root is reached, the items under it are reached already, so they leave the list.
	fx.setPerm(t, fx.rootOf(t, fx.fld.ID), fx.bob.ID, "viewer")
	shared = decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/shared", nil))
	if len(shared.Items) != 0 {
		t.Errorf("shared after the root was shared = %+v, want none", shared.Items)
	}
	// An account key has no shares.
	wantError(t, call(t, fx.s, "GET", "/api/v1/shared", fx.key, nil), 403, apierr.Forbidden)
}

func TestPermissionsAPIRules(t *testing.T) {
	fx := newSharingFixture(t)
	perms := "/api/v1/nodes/" + fx.b.ID + "/permissions/" + fx.bob.ID

	// An editor reads and changes content but may not manage permissions.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "editor")
	wantError(t, callSession(t, fx.s, fx.annSess, "PUT", perms, map[string]any{"role": "viewer"}), 403, apierr.Forbidden)

	// A manager sets a role on a node, and the answer carries the role.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	rec := callSession(t, fx.s, fx.annSess, "PUT", perms, map[string]any{"role": "contributor"})
	wantStatus(t, rec, 200)
	if got := decode[permissionJSON](t, rec); got.Role != "contributor" || got.SubjectID != fx.bob.ID {
		t.Errorf("permission set = %+v", got)
	}
	wantError(t, call(t, fx.s, "PUT", perms, fx.key, map[string]any{"role": "lead"}), 400, apierr.InvalidRequest)

	// A subject of another account is not found.
	acc2, key2 := newAccount(t, fx.s, fx.admin, "Other")
	other, _ := verifiedUser(t, fx.s, key2, acc2.ID, "x@example.com")
	wantError(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+other.ID, fx.key,
		map[string]any{"role": "viewer"}), 404, apierr.NotFound)

	// A token never manages permissions, even one that acts for a manager.
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID},
		"user": fx.ann.ID, "permissions": "write"})
	wantError(t, call(t, fx.s, "PUT", perms, tok, map[string]any{"role": "viewer"}), 403, apierr.Forbidden)

	// An installation admin is refused.
	wantError(t, call(t, fx.s, "PUT", perms, fx.admin, map[string]any{"role": "viewer"}), 403, apierr.Forbidden)

	// A removal is audited, and a second one finds nothing to remove.
	wantStatus(t, call(t, fx.s, "DELETE", perms, fx.key, nil), 204)
	wantError(t, call(t, fx.s, "DELETE", perms, fx.key, nil), 404, apierr.NotFound)
	audit := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	seen := map[string]bool{}
	for _, e := range audit.Items {
		seen[e.Action] = true
	}
	if !seen["permission.set"] {
		t.Errorf("audit has no permission.set entry")
	}
}

func TestTokenNodeScope(t *testing.T) {
	fx := newSharingFixture(t)
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID},
		"path": "/a/b", "permissions": "write"})
	get := func(id string) *httptest.ResponseRecorder { return getWith(t, fx.s, tok, "/api/v1/nodes/"+id) }

	wantStatus(t, get(fx.deep.ID), 200)
	wantError(t, get(fx.sib.ID), 404, apierr.NotFound)
	wantError(t, get(fx.a.ID), 404, apierr.NotFound)
	// Write inside the directory works; a move out of it, or into it from outside, does not.
	wantStatus(t, rawCall(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.deep.ID, tok, `{"name":"renamed.txt"}`,
		map[string]string{"Content-Type": "application/json"}), 200)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/move", tok,
		map[string]any{"parent": fx.c.ID}), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/move", tok,
		map[string]any{"parent": fx.b.ID}), 404, apierr.NotFound)

	// A read token caps the role at viewer, whatever the user holds.
	readTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "node": fx.b.ID})
	wantStatus(t, rawCall(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.deep.ID, readTok, `{"name":"x.txt"}`,
		map[string]string{"Content-Type": "application/json"}), 403)

	// A path needs exactly one folder, and a path that names nothing is not found.
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/tokens", fx.key, map[string]any{
		"folders": []string{fx.fld.ID, other.ID}, "path": "/a"})
	wantError(t, rec, 400, apierr.InvalidRequest)
	wantError(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/tokens", fx.key, map[string]any{
		"folders": []string{fx.fld.ID}, "path": "/nothing"}), 404, apierr.NotFound)
}

func TestDeletingUserWithFilesNeedsDeleteFiles(t *testing.T) {
	fx := newSharingFixture(t)
	carol, carolSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "carol@example.com")
	pf := fx.personalFolder(t, carolSess)
	wantStatus(t, rawSession(t, fx.s, carolSess, "POST", "/api/v1/folders/"+pf+"/files?"+q("/f.txt"), "hello", nil), 201)
	before := usageOf(t, fx.s, fx.key, fx.acc.ID).UsedBytes

	wantError(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+carol.ID, fx.key, nil),
		409, apierr.Conflict)
	if got := usageOf(t, fx.s, fx.key, fx.acc.ID).UsedBytes; got != before {
		t.Errorf("refused delete changed usage: %d -> %d", before, got)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+carol.ID+"?delete_files=true", fx.key, nil), 204)
	if got := usageOf(t, fx.s, fx.key, fx.acc.ID).UsedBytes; got != before-5 {
		t.Errorf("usage after delete_files = %d, want %d", got, before-5)
	}

	// An empty personal folder goes without the flag.
	dave, _ := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dave@example.com")
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+dave.ID, fx.key, nil), 204)
}

func TestGrantsAliasRoundTrip(t *testing.T) {
	fx := newSharingFixture(t)
	grants := "/api/v1/accounts/" + fx.acc.ID + "/folders/" + fx.fld.ID + "/grants/" + fx.ann.ID
	put := func(level string) {
		t.Helper()
		got := decode[grantJSON](t, call(t, fx.s, "PUT", grants, fx.key, map[string]any{"level": level}))
		if got.Level != level {
			t.Errorf("PUT %s answered level %q", level, got.Level)
		}
	}
	put("read")
	perms := decode[listResponse[permissionJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.rootOf(t, fx.fld.ID)+"/permissions", fx.key, nil))
	if len(perms.Items) != 1 || perms.Items[0].Role != "viewer" {
		t.Errorf("read grant as a permission = %+v, want viewer", perms.Items)
	}
	put("write")
	list := decode[listResponse[grantJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+fx.fld.ID+"/grants", fx.key, nil))
	if len(list.Items) != 1 || list.Items[0].UserID != fx.ann.ID || list.Items[0].Level != "write" {
		t.Errorf("folder grants = %+v", list.Items)
	}
	mine := decode[listResponse[grantJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"/grants", fx.key, nil))
	if len(mine.Items) != 1 || mine.Items[0].FolderID != fx.fld.ID || mine.Items[0].Level != "write" {
		t.Errorf("user grants = %+v", mine.Items)
	}
	// A personal folder never takes a grant.
	pf := fx.personalFolder(t, fx.annSess)
	wantError(t, call(t, fx.s, "PUT", "/api/v1/accounts/"+fx.acc.ID+"/folders/"+pf+"/grants/"+fx.bob.ID, fx.key,
		map[string]any{"level": "read"}), 404, apierr.NotFound)
	wantStatus(t, call(t, fx.s, "DELETE", grants, fx.key, nil), 204)
	wantError(t, call(t, fx.s, "DELETE", grants, fx.key, nil), 404, apierr.NotFound)
}

// TestMoveKeepsPermissionsCopyDoesNot: a move keeps the permissions on the node it moves, since the
// node keeps its ID; a copy is a new node and takes none of the original's permissions.
func TestMoveKeepsPermissionsCopyDoesNot(t *testing.T) {
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/move", fx.key, map[string]any{"parent": fx.c.ID}), 200)
	perms := decode[listResponse[permissionJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.b.ID+"/permissions", fx.key, nil))
	if len(perms.Items) != 1 || perms.Items[0].SubjectID != fx.bob.ID {
		t.Errorf("permissions after a move = %+v, want bob's", perms.Items)
	}

	fx.setPerm(t, fx.sib.ID, fx.bob.ID, "viewer")
	copied := decode[nodeJSON](t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.sib.ID+"/copy", fx.key,
		map[string]any{"parent": fx.a.ID, "name": "copy.txt"}))
	if perms := decode[listResponse[permissionJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+copied.ID+"/permissions", fx.key, nil)); len(perms.Items) != 0 {
		t.Errorf("permissions on a copy = %+v, want none", perms.Items)
	}
}

// TestTokenCannotReadPermissions: a delegated token, read or write, may not list the permissions on a
// node. Permissions are a management view, and a token manages nothing.
func TestTokenCannotReadPermissions(t *testing.T) {
	fx := newSharingFixture(t)
	for _, body := range []map[string]any{
		{"folders": []string{fx.fld.ID}, "node": fx.b.ID},
		{"folders": []string{fx.fld.ID}, "node": fx.b.ID, "permissions": "write"},
	} {
		tok := mintFor(t, fx.s, fx.key, fx.acc.ID, body)
		wantError(t, getWith(t, fx.s, tok, "/api/v1/nodes/"+fx.b.ID+"/permissions"), 403, apierr.Forbidden)
	}
}

// TestPermissionListingShowsOnlyOwnRowsBelowManager: a user below manager sees the permission rows that
// name it or a group it is in, and the inherited ones among those only. Managers and the account key
// see the full chain.
func TestPermissionListingShowsOnlyOwnRowsBelowManager(t *testing.T) {
	fx := newSharingFixture(t)
	grp := fx.group(t, "Staff")
	fx.inGroup(t, grp, fx.ann.ID, true)
	fx.setPerm(t, fx.a.ID, fx.bob.ID, "viewer")
	fx.setPerm(t, fx.a.ID, grp, "viewer")
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	subjects := func(rec *httptest.ResponseRecorder) map[string]bool {
		t.Helper()
		wantStatus(t, rec, 200)
		got := map[string]bool{}
		for _, pm := range decode[listResponse[permissionJSON]](t, rec).Items {
			got[pm.SubjectID] = true
		}
		return got
	}
	list := "/api/v1/nodes/" + fx.b.ID + "/permissions"

	annSeen := subjects(callSession(t, fx.s, fx.annSess, "GET", list, nil))
	if !annSeen[fx.ann.ID] || !annSeen[grp] || annSeen[fx.bob.ID] {
		t.Errorf("viewer sees subjects %v, want its own row and its group's, not bob's", annSeen)
	}
	if keySeen := subjects(call(t, fx.s, "GET", list, fx.key, nil)); !keySeen[fx.bob.ID] {
		t.Errorf("account key does not see the full chain: %v", keySeen)
	}
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "manager")
	if mgr := subjects(callSession(t, fx.s, fx.annSess, "GET", list, nil)); !mgr[fx.bob.ID] {
		t.Errorf("manager does not see the full chain: %v", mgr)
	}
}

// TestAuditHidesPersonalFolderEntries: an entry about a file in a personal folder is private to the
// folder's user. The owner does not see it; the account key sees all of it.
func TestAuditHidesPersonalFolderEntries(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.bob.ID, fx.key,
		map[string]any{"role": "owner"}), 200)
	pf := fx.personalFolder(t, fx.annSess)
	rec := rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+pf+"/files?"+q("/mine.txt"), "hello", nil)
	wantStatus(t, rec, 201)
	id := decode[nodeJSON](t, rec).ID
	wantStatus(t, rawSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/nodes/"+id, `{"name":"renamed.txt"}`,
		map[string]string{"Content-Type": "application/json"}), 200)

	actions := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		wantStatus(t, rec, 200)
		var acts []string
		for _, e := range decode[listResponse[auditJSON]](t, rec).Items {
			if e.Target == id {
				acts = append(acts, e.Action)
			}
		}
		return acts
	}
	if got := actions(callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", nil)); len(got) != 0 {
		t.Errorf("owner sees entries about a personal file: %v", got)
	}
	got := actions(call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	if !slices.Contains(got, "node.create") || !slices.Contains(got, "node.rename") {
		t.Errorf("account key entries about the file = %v, want node.create and node.rename", got)
	}
}

// TestEveryNodeWriteKeepsItsOwner: a node made by a write keeps its creator as owner, whichever way
// it was made. The owner must not be wiped by a later update.
func TestEveryNodeWriteKeepsItsOwner(t *testing.T) {
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "contributor")
	ownerOf := func(id string) string {
		t.Helper()
		n, err := fx.s.store.Node(context.Background(), fx.acc.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		return n.OwnerID
	}
	check := func(what, id string) {
		t.Helper()
		if got := ownerOf(id); got != fx.ann.ID {
			t.Errorf("owner after %s = %q, want %q", what, got, fx.ann.ID)
		}
	}

	rec := rawSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fx.fld.ID+"/files?"+q("/a/c/new.txt"), "x", nil)
	wantStatus(t, rec, 201)
	check("a small file create", decode[nodeJSON](t, rec).ID)

	rec = callSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fx.fld.ID+"/directories", map[string]any{"path": "/a/c/dir"})
	wantStatus(t, rec, 201)
	check("a mkdir", decode[nodeJSON](t, rec).ID)

	rec = callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.sib.ID+"/copy", map[string]any{"parent": fx.b.ID, "name": "copy.txt"})
	wantStatus(t, rec, 201)
	check("a copy", decode[nodeJSON](t, rec).ID)

	up := upSession(t, fx.s, fx.annSess, "POST", "/api/v1/uploads", map[string]string{
		"Upload-Length":   "3",
		"Upload-Metadata": md("folder", fx.fld.ID, "parent", fx.c.ID, "name", "up.txt"),
	}, nil, 0)
	wantStatus(t, up, 201)
	if patch := upSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/uploads/"+uploadID(t, up), map[string]string{
		"Upload-Offset": "0", "Content-Type": "application/offset+octet-stream",
	}, strings.NewReader("abc"), 3); patch.Code/100 != 2 {
		t.Fatalf("upload completion: %d %s", patch.Code, patch.Body)
	}
	kids := decode[listResponse[nodeJSON]](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", nil))
	for _, k := range kids.Items {
		if k.Name == "up.txt" {
			check("an upload completion", k.ID)
		}
	}
}

// TestUnreachableFileOnPathAnswersLikeMissing: a user who reaches neither the directory holding a file
// nor the file learns nothing from a path through it. The answer is the one a missing name gets. The
// account key, which reaches everything, is told the path is invalid.
func TestUnreachableFileOnPathAnswersLikeMissing(t *testing.T) {
	fx := newSharingFixture(t)
	putNode(t, fx.s, fx.key, fx.fld.ID, "/a/f.txt", "file")
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer") // deep only: /a and /a/f.txt stay closed
	list := func(p string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, fx.annSess, "GET", childrenPath(fx.fld.ID)+"?"+q(p), nil)
	}
	mkdir := func(p string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, fx.annSess, "POST", "/api/v1/folders/"+fx.fld.ID+"/directories", map[string]any{"path": p})
	}

	missL := wantError(t, list("/a/missing/zz"), 404, apierr.NotFound)
	fileL := wantError(t, list("/a/f.txt/zz"), 404, apierr.NotFound)
	if fileL.Message != missL.Message {
		t.Errorf("listing through a file says %q, a missing name says %q", fileL.Message, missL.Message)
	}
	missM := wantError(t, mkdir("/a/missing/zz"), 404, apierr.NotFound)
	fileM := wantError(t, mkdir("/a/f.txt/zz"), 404, apierr.NotFound)
	if fileM.Message != missM.Message {
		t.Errorf("mkdir through a file says %q, a missing name says %q", fileM.Message, missM.Message)
	}

	wantError(t, call(t, fx.s, "GET", childrenPath(fx.fld.ID)+"?"+q("/a/f.txt/zz"), fx.key, nil), 400, apierr.InvalidPath)
	wantError(t, call(t, fx.s, "POST", "/api/v1/folders/"+fx.fld.ID+"/directories", fx.key,
		map[string]any{"path": "/a/f.txt/zz"}), 400, apierr.InvalidPath)
}

// TestReplaceNeedsEditorOnDisplacedFile: a contributor may place a file but may not overwrite one, so
// a replace that displaces a file needs editor on that file.
func TestReplaceNeedsEditorOnDisplacedFile(t *testing.T) {
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "contributor")
	replace := map[string]any{"parent": fx.c.ID, "name": "x.txt", "on_conflict": "replace"}

	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy", replace), 403, apierr.Forbidden)
	kids := decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", fx.key, nil))
	if len(kids.Items) != 1 || kids.Items[0].ID != fx.sib.ID {
		t.Errorf("refused replace changed the directory: %+v", kids.Items)
	}

	fx.setPerm(t, fx.c.ID, fx.ann.ID, "editor")
	if rec := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy", replace); rec.Code/100 != 2 {
		t.Fatalf("replace as editor: %d %s", rec.Code, rec.Body)
	}
	kids = decode[listResponse[nodeJSON]](t, call(t, fx.s, "GET", "/api/v1/nodes/"+fx.c.ID+"/children", fx.key, nil))
	if len(kids.Items) != 1 || kids.Items[0].ID == fx.sib.ID {
		t.Errorf("replace as editor left %+v, want the displaced file gone", kids.Items)
	}
}

// TestQueuedCopyNeedsItsTokenNode: a copy job queued by one token is not visible to another token of the
// same user that is scoped to a different node.
func TestQueuedCopyNeedsItsTokenNode(t *testing.T) {
	fx := newSharingFixture(t)
	fx.s.copySyncNodes = 0 // every copy becomes a job
	mint := func(node string) string {
		return mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID},
			"node": node, "permissions": "write"})
	}
	tokA, tokB := mint(fx.a.ID), mint(fx.c.ID)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.deep.ID+"/copy", tokA, map[string]any{"parent": fx.b.ID, "name": "copy.txt"})
	wantStatus(t, rec, 202)
	jobPath := "/api/v1/jobs/" + path.Base(rec.Header().Get("Location"))
	wantStatus(t, getWith(t, fx.s, tokA, jobPath), 200)
	wantError(t, getWith(t, fx.s, tokB, jobPath), 404, apierr.NotFound)
}

// TestDeleteUserConflictOmitsEntryCount: the refusal to delete a user with files does not say how many
// entries the folder holds.
func TestDeleteUserConflictOmitsEntryCount(t *testing.T) {
	fx := newSharingFixture(t)
	carol, carolSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "carol@example.com")
	pf := fx.personalFolder(t, carolSess)
	wantStatus(t, rawSession(t, fx.s, carolSess, "POST", "/api/v1/folders/"+pf+"/files?"+q("/f.txt"), "hello", nil), 201)

	e := wantError(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+carol.ID, fx.key, nil), 409, apierr.Conflict)
	if _, ok := e.Details["entries"]; ok {
		t.Errorf("conflict details name the entry count: %v", e.Details)
	}
	if want := "the user's personal folder is not empty; pass delete_files=true or transfer_to"; e.Message != want {
		t.Errorf("conflict message = %q, want %q", e.Message, want)
	}
}
