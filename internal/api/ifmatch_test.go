// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestIfMatchOnRenameAndMove checks the optional If-Match on PATCH and move: a match changes the node,
// a mismatch is precondition_failed and changes nothing, and an absent header checks nothing. Files and
// directories both carry the token, and each write changes it.
func TestIfMatchOnRenameAndMove(t *testing.T) {
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	one := newFolder(t, s, key, acc.ID, "One")
	two := newFolder(t, s, key, acc.ID, "Two")
	dir := mkdirs(t, s, key, one.ID, "/dir")
	f := putNode(t, s, key, one.ID, "/f.txt", "abc")
	node := "/api/v1/nodes/"
	withIfMatch := func(etag string) map[string]string {
		return map[string]string{"If-Match": etag, "Content-Type": "application/json"}
	}

	if f.CreatedBy == "" || f.OwnerID == "" {
		t.Errorf("a node carries no creator or owner: %+v", f)
	}
	if f.ETag == "" || dir.ETag == "" {
		t.Fatalf("files and directories must carry an etag: file %q, dir %q", f.ETag, dir.ETag)
	}

	// Rename with a matching ETag succeeds, and the ETag changes with the write.
	renamed := decode[nodeJSON](t, rawCall(t, s, "PATCH", node+f.ID, key, `{"name":"g.txt"}`, withIfMatch(f.ETag)))
	if renamed.Name != "g.txt" || renamed.ETag == f.ETag {
		t.Fatalf("rename with a matching If-Match: %+v", renamed)
	}

	// A stale ETag is refused, and the name stays.
	rec := rawCall(t, s, "PATCH", node+f.ID, key, `{"name":"h.txt"}`, withIfMatch(f.ETag))
	wantError(t, rec, 412, apierr.PreconditionFailed)
	if got := decode[nodeJSON](t, call(t, s, "GET", node+f.ID, key, nil)); got.Name != "g.txt" {
		t.Errorf("a refused rename changed the name to %q", got.Name)
	}

	// Without If-Match nothing is checked.
	wantStatus(t, rawCall(t, s, "PATCH", node+f.ID, key, `{"name":"h.txt"}`, map[string]string{"Content-Type": "application/json"}), 200)

	// A move with a stale ETag is refused and changes nothing.
	stale := renamed.ETag
	wantError(t, rawCall(t, s, "POST", node+f.ID+"/move", key, `{"folder":"`+two.ID+`"}`, withIfMatch(stale)),
		412, apierr.PreconditionFailed)
	if got := decode[nodeJSON](t, call(t, s, "GET", node+f.ID, key, nil)); got.FolderID != one.ID {
		t.Errorf("a refused move changed the folder to %s", got.FolderID)
	}
	// A move with the current ETag goes through.
	current := decode[nodeJSON](t, call(t, s, "GET", node+f.ID, key, nil)).ETag
	moved := decode[nodeJSON](t, rawCall(t, s, "POST", node+f.ID+"/move", key, `{"folder":"`+two.ID+`"}`, withIfMatch(current)))
	if moved.FolderID != two.ID {
		t.Fatalf("move with a matching If-Match: %+v", moved)
	}

	// Directories: a rename with the directory's own token works, and a stale token is refused.
	// A directory's token comes from its modified time in milliseconds: wait so the rename below is a
	// new version, not the same millisecond as the creation.
	time.Sleep(2 * time.Millisecond)
	dirTag := dir.ETag
	wantStatus(t, rawCall(t, s, "PATCH", node+dir.ID, key, `{"name":"dir2"}`, withIfMatch(dirTag)), 200)
	wantError(t, rawCall(t, s, "PATCH", node+dir.ID, key, `{"name":"dir3"}`, withIfMatch(dirTag)),
		412, apierr.PreconditionFailed)
	// A malformed If-Match is no match, not a crash.
	wantError(t, rawCall(t, s, "POST", node+dir.ID+"/move", key, `{"folder":"`+two.ID+`"}`, withIfMatch("*")),
		412, apierr.PreconditionFailed)
}
