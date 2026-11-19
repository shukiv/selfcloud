// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// changesStartJSON is the answer of GET /changes/start.
type changesStartJSON struct {
	Cursor string `json:"cursor"`
}

// changeEntryJSON is one folded entry of the feed.
type changeEntryJSON struct {
	NodeID  string    `json:"node_id"`
	Removed bool      `json:"removed"`
	Relist  bool      `json:"relist"`
	Node    *nodeJSON `json:"node"`
}

// changesJSON is the answer of GET /changes.
type changesJSON struct {
	Changes []changeEntryJSON `json:"changes"`
	Cursor  string            `json:"cursor"`
	HasMore bool              `json:"has_more"`
}

// startCursor asks for a starting cursor of a space (or of the account when space is "").
func startCursor(t *testing.T, s *Server, cred, space string) string {
	t.Helper()
	p := "/api/v1/changes/start"
	if space != "" {
		p += "?space=" + url.QueryEscape(space)
	}
	rec := call(t, s, "GET", p, cred, nil)
	wantStatus(t, rec, 200)
	return decode[changesStartJSON](t, rec).Cursor
}

// feedCall reads the feed with the given query values, returning the raw recorder.
func feedCall(t *testing.T, s *Server, cred string, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, s, "GET", "/api/v1/changes?"+q.Encode(), cred, nil)
}

// feed reads the feed of a space (or account) after a cursor.
func feed(t *testing.T, s *Server, cred, space, cursor string) changesJSON {
	t.Helper()
	q := url.Values{"cursor": {cursor}, "limit": {"1000"}}
	if space != "" {
		q.Set("space", space)
	}
	rec := feedCall(t, s, cred, q)
	wantStatus(t, rec, 200)
	return decode[changesJSON](t, rec)
}

// entryOf finds the entry for a node, or nil.
func entryOf(out changesJSON, nodeID string) *changeEntryJSON {
	for i := range out.Changes {
		if out.Changes[i].NodeID == nodeID {
			return &out.Changes[i]
		}
	}
	return nil
}

// cursorSeq opens a cursor on the server, as a request does, and returns the seq it names. A cursor that does not
// open for the feed fails the test.
func cursorSeq(t *testing.T, s *Server, feed, tok string) int64 {
	t.Helper()
	seq, err := s.openChangesCursor(feed, tok)
	if err != nil {
		t.Fatalf("cursor %q does not open for %s: %v", tok, feed, err)
	}
	return seq
}

// sealCursor makes a cursor naming the feed and seq, as the server does.
func sealCursor(t *testing.T, s *Server, feed string, seq int64) string {
	t.Helper()
	tok, err := s.sealChangesCursor(feed, seq)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestChangesStartThenFeed: start gives a cursor at the journal's head; a write after it shows up once, with the node
// as a stat answers it; the next read after the new cursor is empty, and changes is [] never null.
func TestChangesStartThenFeed(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	seq := cursorSeq(t, fx.s, fx.fld.ID, cur)
	head, err := fx.s.store.NodeChangesHead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if seq != head {
		t.Errorf("start seq = %d, want the head %d", seq, head)
	}

	n := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/new.txt", "hello")
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur)
	if len(out.Changes) != 1 {
		t.Fatalf("changes = %+v, want one", out.Changes)
	}
	e := out.Changes[0]
	if e.NodeID != n.ID || e.Removed || e.Relist {
		t.Fatalf("entry = %+v, want a live entry for %s", e, n.ID)
	}
	if e.Node == nil || e.Node.ID != n.ID || e.Node.Role != "manager" {
		t.Fatalf("entry node = %+v, want the node with manager role", e.Node)
	}
	if out.HasMore {
		t.Error("has_more set with nothing left")
	}
	if out.Cursor == cur {
		t.Error("the cursor did not advance")
	}

	// The node in the entry matches a stat of it.
	stat := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+n.ID, fx.key, nil))
	if e.Node.Name != stat.Name || e.Node.Kind != stat.Kind || e.Node.Size != stat.Size {
		t.Errorf("entry node %+v differs from stat %+v", e.Node, stat)
	}

	// Reading after the new cursor is empty, and the array is present.
	rec := feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {out.Cursor}})
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"changes":[]`) {
		t.Errorf("empty feed body = %s, want changes:[]", rec.Body)
	}
	if got := decode[changesJSON](t, rec); len(got.Changes) != 0 || got.Cursor != out.Cursor {
		t.Errorf("empty feed = %+v, want no changes and the same cursor", got)
	}
}

// TestChangesFoldsWritesToOneEntry: several writes of one node fold to a single entry carrying the last name, and the
// cursor advances to the last raw row.
func TestChangesFoldsWritesToOneEntry(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.deep.ID, fx.key, map[string]any{"name": name}), 200)
	}
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur)
	if len(out.Changes) != 1 {
		t.Fatalf("changes = %+v, want one folded entry", out.Changes)
	}
	e := out.Changes[0]
	if e.NodeID != fx.deep.ID || e.Node == nil || e.Node.Name != "three.txt" {
		t.Fatalf("entry = %+v, want the last name three.txt", e)
	}
}

// TestChangesRemoved: a trashed node, a purged node, a child of a trashed folder, and a node moved out of the space all
// report removed with no node.
func TestChangesRemoved(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)

	// Trashed.
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.sib.ID, fx.key, nil), 204)
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur)
	e := entryOf(out, fx.sib.ID)
	if e == nil || !e.Removed || e.Node != nil {
		t.Fatalf("trashed entry = %+v, want removed with no node", e)
	}

	// Purged from the trash.
	cur = startCursor(t, fx.s, fx.key, fx.fld.ID)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+fx.sib.ID, fx.key, nil), 204)
	out = feed(t, fx.s, fx.key, fx.fld.ID, cur)
	e = entryOf(out, fx.sib.ID)
	if e == nil || !e.Removed || e.Node != nil {
		t.Fatalf("purged entry = %+v, want removed with no node", e)
	}

	// A child whose own row changed is reported removed once an ancestor is trashed, though the child was not trashed
	// itself. (A child with no row of its own is not listed: the folder's removal stands for the subtree.)
	cur = startCursor(t, fx.s, fx.key, fx.fld.ID)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.deep.ID, fx.key, map[string]any{"name": "renamed.txt"}), 200)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
	out = feed(t, fx.s, fx.key, fx.fld.ID, cur)
	for _, id := range []string{fx.b.ID, fx.deep.ID} {
		e = entryOf(out, id)
		if e == nil || !e.Removed || e.Node != nil {
			t.Fatalf("entry for %s = %+v, want removed with no node", id, e)
		}
	}

	// A node moved to another folder leaves the space, and the destination's feed reports it live. A fresh node is used
	// because the folder trashed above holds the fixture's deep file.
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Elsewhere")
	mover := putNode(t, fx.s, fx.key, fx.fld.ID, "/mover.txt", "x")
	destCur := startCursor(t, fx.s, fx.key, other.ID)
	cur = startCursor(t, fx.s, fx.key, fx.fld.ID)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+mover.ID+"/move", fx.key,
		map[string]any{"folder": other.ID}), 200)
	out = feed(t, fx.s, fx.key, fx.fld.ID, cur)
	e = entryOf(out, mover.ID)
	if e == nil || !e.Removed || e.Node != nil {
		t.Fatalf("moved-out entry = %+v, want removed with no node", e)
	}
	out = feed(t, fx.s, fx.key, other.ID, destCur)
	e = entryOf(out, mover.ID)
	if e == nil || e.Removed || e.Node == nil {
		t.Fatalf("destination entry = %+v, want the node live", e)
	}
}

// TestChangesRelistOnRestore: a folder back from the trash reports relist, since its children's rows did not change.
func TestChangesRelistOnRestore(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID, fx.key, nil), 204)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+fx.b.ID+"/restore", fx.key, nil), 200)
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur)
	e := entryOf(out, fx.b.ID)
	if e == nil || !e.Relist || e.Removed {
		t.Fatalf("restored folder entry = %+v, want relist and live", e)
	}
}

// TestChangesPaging: a limit walks the journal in pages, each node once, the cursor advancing, has_more until the last.
func TestChangesPaging(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	cur0 := cur
	var ids []string
	for i := 0; i < 5; i++ {
		n := putNode(t, fx.s, fx.key, fx.fld.ID, fmt.Sprintf("/page%d.txt", i), "x")
		ids = append(ids, n.ID)
	}
	seen := map[string]bool{}
	pages := 0
	for {
		q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "limit": {"2"}}
		rec := feedCall(t, fx.s, fx.key, q)
		wantStatus(t, rec, 200)
		out := decode[changesJSON](t, rec)
		pages++
		if len(out.Changes) == 0 {
			break
		}
		for _, e := range out.Changes {
			if seen[e.NodeID] {
				t.Fatalf("node %s reported twice", e.NodeID)
			}
			seen[e.NodeID] = true
		}
		cur = out.Cursor
		if !out.HasMore {
			break
		}
		if pages > 10 {
			t.Fatal("paging did not end")
		}
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("node %s never reported", id)
		}
	}
	// A file leaves two journal rows (its insert and the update that lands its content), so the page count follows the
	// raw rows, not the nodes: ceil(rows / limit).
	startSeq := cursorSeq(t, fx.s, fx.fld.ID, cur0)
	rows, err := fx.s.store.NodeChangesAfter(context.Background(), fx.acc.ID, fx.fld.ID, startSeq, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if wantPages := (len(rows) + 1) / 2; pages != wantPages {
		t.Errorf("pages = %d, want %d for %d raw rows at limit 2", pages, wantPages, len(rows))
	}
}

// wantGoneReset checks a 410 gone answer with reason reset.
func wantGoneReset(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e := wantError(t, rec, 410, apierr.Gone)
	if e.Details["reason"] != "reset" {
		t.Errorf("gone reason = %v, want reset", e.Details["reason"])
	}
}

// TestChangesCursorOfAnotherFeed: a cursor sealed for one feed does not open for another, and neither does a garbage
// cursor: both are gone with reason reset, telling the client to start again. A missing cursor is a bad request.
func TestChangesCursorOfAnotherFeed(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	curA := startCursor(t, fx.s, fx.key, fx.fld.ID)

	rec := feedCall(t, fx.s, fx.key, url.Values{"space": {other.ID}, "cursor": {curA}})
	wantGoneReset(t, rec)

	rec = feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {"not-a-cursor"}})
	wantGoneReset(t, rec)

	acctCur := startCursor(t, fx.s, fx.key, "")
	rec = feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {acctCur}})
	wantGoneReset(t, rec)

	rec = feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}})
	wantError(t, rec, 400, apierr.InvalidRequest)
}

// TestChangesCursorHidesTheCounter: the token body is sealed, not readable: it carries no feed id or seq in the
// clear, and two cursors naming the same feed and seq differ, so one tenant cannot read another's write volume.
func TestChangesCursorHidesTheCounter(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	seq := cursorSeq(t, fx.s, fx.fld.ID, cur)
	body := strings.TrimPrefix(cur, "c1_")
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("cursor body %q: %v", body, err)
	}
	if strings.Contains(string(raw), fx.fld.ID) || strings.Contains(string(raw), fmt.Sprintf(`"q":%d`, seq)) {
		t.Errorf("the cursor body carries the feed or the seq in the clear: %q", raw)
	}
	var pl map[string]any
	if err := json.Unmarshal(raw, &pl); err == nil {
		t.Errorf("the cursor body is plain JSON: %q", raw)
	}
	again := sealCursor(t, fx.s, fx.fld.ID, seq)
	if again == cur {
		t.Error("two cursors of the same feed and seq are identical; the seal carries no nonce")
	}
	if got := cursorSeq(t, fx.s, fx.fld.ID, again); got != seq {
		t.Errorf("the second cursor names seq %d, want %d", got, seq)
	}
}

// TestChangesCursorAfterKeyRotation: a rotated links key leaves every old cursor unable to open; the answer is gone
// with reason reset, and a fresh start reads again.
func TestChangesCursorAfterKeyRotation(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	putNode(t, fx.s, fx.key, fx.fld.ID, "/rot.txt", "x")
	fx.s.links.current = bytes.Repeat([]byte{9}, 32)
	rec := feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {cur}})
	wantGoneReset(t, rec)
	cur2 := startCursor(t, fx.s, fx.key, fx.fld.ID)
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur2)
	if len(out.Changes) != 0 {
		t.Errorf("a fresh start after the rotation reads %+v, want empty", out.Changes)
	}
}

// TestChangesCursorNotBoundToTheCaller: a cursor opens for any caller of the same feed, since the feed itself is
// access-checked: another member with root reach, and a delegated token acting as the first member, both read it.
func TestChangesCursorNotBoundToTheCaller(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "viewer")
	fx.setPerm(t, root, fx.bob.ID, "viewer")
	rec := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/changes/start?space="+fx.fld.ID, nil)
	wantStatus(t, rec, 200)
	cur := decode[changesStartJSON](t, rec).Cursor
	n := putNode(t, fx.s, fx.key, fx.fld.ID, "/shared.txt", "x")

	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}}
	rec = callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/changes?"+q.Encode(), nil)
	wantStatus(t, rec, 200)
	if entryOf(decode[changesJSON](t, rec), n.ID) == nil {
		t.Error("the other member's read of the same cursor missed the change")
	}

	annTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID})
	rec = feedCall(t, fx.s, annTok, q)
	wantStatus(t, rec, 200)
	if entryOf(decode[changesJSON](t, rec), n.ID) == nil {
		t.Error("the delegated token's read of the same cursor missed the change")
	}
}

// TestChangesBelowMarkIsGone: a cursor whose seq fell below the prune mark is refused with gone and reason reset.
func TestChangesBelowMarkIsGone(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	// A change after the cursor, then prune everything so the mark passes the cursor's seq.
	putNode(t, fx.s, fx.key, fx.fld.ID, "/after.txt", "x")
	if _, err := fx.s.store.PruneNodeChanges(context.Background(), time.Now().Add(time.Hour), 10000); err != nil {
		t.Fatal(err)
	}
	rec := feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {cur}})
	wantGoneReset(t, rec)
}

// TestChangesStartAfterFullPrune: when every row has aged out, a fresh start still answers a cursor at the head,
// and reading after it is an empty page, not gone: the head never lowers with the rows.
func TestChangesStartAfterFullPrune(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	putNode(t, fx.s, fx.key, fx.fld.ID, "/aged.txt", "x")
	for {
		n, err := fx.s.store.PruneNodeChanges(context.Background(), time.Now().Add(time.Hour), 10000)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	// The old cursor fell below the mark.
	rec := feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {cur}})
	wantGoneReset(t, rec)
	// A fresh start names the head, at the mark, and reads an empty page.
	cur2 := startCursor(t, fx.s, fx.key, fx.fld.ID)
	if cursorSeq(t, fx.s, fx.fld.ID, cur2) == 0 {
		t.Error("start after a full prune names seq 0, want the head the journal keeps")
	}
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur2)
	if len(out.Changes) != 0 || out.Cursor != cur2 {
		t.Errorf("read after the fresh start = %+v, want empty with the same cursor", out)
	}
}

// TestChangesCursorAboveHeadIsGone: a cursor naming a seq above the head (as one kept across a restore from an
// older backup) is refused with gone and reason reset, not served an empty page.
func TestChangesCursorAboveHeadIsGone(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	seq := cursorSeq(t, fx.s, fx.fld.ID, cur)
	future := sealCursor(t, fx.s, fx.fld.ID, seq+10)
	rec := feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {future}})
	wantGoneReset(t, rec)
}

// TestChangesPruneBetweenMarkAndRows: a prune that lands between the mark read and the row read cannot make the
// feed skip rows silently: both are read in one snapshot, so the rows the cursor still names are reported.
func TestChangesPruneBetweenMarkAndRows(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	n := putNode(t, fx.s, fx.key, fx.fld.ID, "/hook.txt", "x")
	fx.s.changesPruneHook = func() {
		if _, err := fx.s.store.PruneNodeChanges(context.Background(), time.Now().Add(time.Hour), 10000); err != nil {
			t.Error(err)
		}
	}
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur)
	e := entryOf(out, n.ID)
	if e == nil || e.Removed {
		t.Fatalf("the prune between the mark and the rows skipped the change: %+v", out)
	}
}

// TestChangesGoneAccountPruneKeepsTheFeed: pruning a deleted account's young rows leaves the mark where it was, so
// another account's cursor keeps reading its own changes.
func TestChangesGoneAccountPruneKeepsTheFeed(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	n := putNode(t, fx.s, fx.key, fx.fld.ID, "/live.txt", "x")
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.InsertAccount(ctx, &store.Account{ID: "acc_gone", Name: "Gone", Status: store.AccountActive,
			CreatedAt: store.Now(), UpdatedAt: store.Now()}); err != nil {
			return err
		}
		if err := tx.InsertFolder(ctx, &store.Folder{ID: "fld_gone", AccountID: "acc_gone", Name: "G", NameKey: "g",
			CreatedAt: store.Now(), UpdatedAt: store.Now()}, "test"); err != nil {
			return err
		}
		root, err := tx.RootNode(ctx, "acc_gone", "fld_gone")
		if err != nil {
			return err
		}
		return tx.InsertNode(ctx, &store.Node{ID: "nod_gone", AccountID: "acc_gone", FolderID: "fld_gone",
			ParentID: root.ID, Name: "g.txt", NameKey: "g.txt", Kind: store.KindFile, Size: 1, ETag: "e",
			CreatedAt: store.Now(), ModifiedAt: store.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		return tx.DeleteAccount(ctx, "acc_gone")
	}); err != nil {
		t.Fatal(err)
	}
	// A cutoff in the past ages nothing out; only the gone account's rows go.
	if _, err := fx.s.store.PruneNodeChanges(ctx, time.Now().Add(-time.Hour), 1000); err != nil {
		t.Fatal(err)
	}
	if mark, err := fx.s.store.NodeChangesMark(ctx); err != nil || mark != 0 {
		t.Fatalf("mark = %d, %v; want 0 after a gone-account prune", mark, err)
	}
	out := feed(t, fx.s, fx.key, fx.fld.ID, cur)
	if entryOf(out, n.ID) == nil {
		t.Fatalf("the live account's feed lost its change: %+v", out)
	}
}

// TestChangesAccountCursorAdvancesPastStaged: the account feed skips staged rows but its cursor advances past them,
// so the publish's new rows are read after it; a cursor stuck before the staged rows would read them never.
func TestChangesAccountCursorAdvancesPastStaged(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.CreateStagingFolder(ctx, fx.acc.ID, "fld_st", "nod_st_root", store.Now()); err != nil {
			return err
		}
		n := &store.Node{ID: "nod_st_file", AccountID: fx.acc.ID, FolderID: "fld_st", ParentID: "nod_st_root",
			Name: "s.txt", NameKey: "s.txt", Kind: store.KindFile, Size: 1, ETag: "e", CreatedAt: store.Now(), ModifiedAt: store.Now()}
		return tx.InsertNodeNoIndex(ctx, n)
	}); err != nil {
		t.Fatal(err)
	}
	cur := startCursor(t, fx.s, fx.key, "")
	startSeq := cursorSeq(t, fx.s, fx.acc.ID, cur)
	raw, err := fx.s.store.NodeChangesAfter(ctx, fx.acc.ID, "", startSeq, 1000)
	if err != nil || len(raw) == 0 {
		t.Fatalf("staged rows after the start = %d, %v; want some", len(raw), err)
	}
	lastRaw := raw[len(raw)-1].Seq
	out := feed(t, fx.s, fx.key, "", cur)
	if len(out.Changes) != 0 {
		t.Fatalf("staged rows reached the account feed: %+v", out.Changes)
	}
	if out.Cursor == cur {
		t.Fatal("the cursor did not advance past the staged rows")
	}
	if got := cursorSeq(t, fx.s, fx.acc.ID, out.Cursor); got != lastRaw {
		t.Fatalf("cursor seq = %d, want the last raw row %d", got, lastRaw)
	}
	// The publish writes new rows, which the advanced cursor reads.
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.MoveStagedTree(ctx, fx.acc.ID, "fld_st", fx.fld.ID, store.Now()); err != nil {
			return err
		}
		return tx.DropStaging(ctx, fx.acc.ID, "fld_st", "nod_st_root")
	}); err != nil {
		t.Fatal(err)
	}
	out = feed(t, fx.s, fx.key, "", out.Cursor)
	if entryOf(out, "nod_st_file") == nil {
		t.Fatalf("the published node never reached the account feed: %+v", out)
	}
}

// TestChangesSpaceReach: the space feed needs reach on the folder's root. A member without it, one with only a deep
// grant, a token confined below the root, a link visitor, a staged or missing space, and an admin all get the refusal.
func TestChangesSpaceReach(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)

	// No permission: not_found on start and on the feed.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/changes/start?space="+fx.fld.ID, nil), 404, apierr.NotFound)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/changes?space="+fx.fld.ID+"&cursor="+url.QueryEscape(cur), nil), 404, apierr.NotFound)

	// A grant deep in the tree does not reach the root.
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/changes/start?space="+fx.fld.ID, nil), 404, apierr.NotFound)

	// A grant on the root opens the feed.
	fx.setPerm(t, root, fx.ann.ID, "viewer")
	wantStatus(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/changes/start?space="+fx.fld.ID, nil), 200)

	// A token confined below the root cannot read the space feed.
	confined := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "node": fx.b.ID})
	wantError(t, call(t, fx.s, "GET", "/api/v1/changes/start?space="+fx.fld.ID, confined, nil), 404, apierr.NotFound)

	// An admin key may not read files.
	wantError(t, call(t, fx.s, "GET", "/api/v1/changes/start?space="+fx.fld.ID, fx.admin, nil), 403, apierr.Forbidden)

	// A missing space and a malformed one are not_found.
	wantError(t, call(t, fx.s, "GET", "/api/v1/changes/start?space=fld_nope", fx.key, nil), 404, apierr.NotFound)
	wantError(t, call(t, fx.s, "GET", "/api/v1/changes/start?space=not-an-id", fx.key, nil), 404, apierr.NotFound)

	// A staged folder is not a space.
	if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.CreateStagingFolder(context.Background(), fx.acc.ID, "fld_staged", "nod_staged_root", store.Now())
	}); err != nil {
		t.Fatal(err)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/changes/start?space=fld_staged", fx.key, nil), 404, apierr.NotFound)
}

// TestChangesLinkVisitorRefused: a link visitor's credential is refused, the route admits no link.
func TestChangesLinkVisitorRefused(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	link, secret := fx.makeAnyone(t, fx.deep.ID, nil)
	_ = link
	tok := fx.visitAnyone(t, secret, "")
	wantError(t, fx.visitor(t, tok, "GET", "/api/v1/changes/start?space="+fx.fld.ID), 403, apierr.Forbidden)
}

// TestChangesAccountFeed: the account feed is the bare account key's; a session, a delegated token and a token acting
// as its user are refused, and an account cursor is refused on a space feed.
func TestChangesAccountFeed(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	cur := startCursor(t, fx.s, fx.key, "")
	putNode(t, fx.s, fx.key, fx.fld.ID, "/in-team.txt", "x")
	putNode(t, fx.s, fx.key, other.ID, "/in-other.txt", "x")
	out := feed(t, fx.s, fx.key, "", cur)
	if len(out.Changes) != 2 {
		t.Fatalf("account feed = %+v, want two changes across folders", out.Changes)
	}

	// A session user without a space is refused.
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/changes?cursor="+url.QueryEscape(cur), nil), 400, apierr.InvalidRequest)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/changes/start", nil), 400, apierr.InvalidRequest)

	// A delegated token, even acting as the key, is refused the account feed.
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
	wantError(t, call(t, fx.s, "GET", "/api/v1/changes?cursor="+url.QueryEscape(cur), tok, nil), 400, apierr.InvalidRequest)
	// A token acting as its user too.
	utok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID})
	wantError(t, call(t, fx.s, "GET", "/api/v1/changes?cursor="+url.QueryEscape(cur), utok, nil), 400, apierr.InvalidRequest)
}

// TestChangesStagedNotReportedUntilPublish: staged rows never reach a feed; after the publish they do.
func TestChangesStagedNotReportedUntilPublish(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	ctx := context.Background()
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.CreateStagingFolder(ctx, fx.acc.ID, "fld_st", "nod_st_root", store.Now()); err != nil {
			return err
		}
		n := &store.Node{ID: "nod_st_file", AccountID: fx.acc.ID, FolderID: "fld_st", ParentID: "nod_st_root",
			Name: "s.txt", NameKey: "s.txt", Kind: store.KindFile, Size: 1, ETag: "e", CreatedAt: store.Now(), ModifiedAt: store.Now()}
		return tx.InsertNodeNoIndex(ctx, n)
	}); err != nil {
		t.Fatal(err)
	}
	cur := startCursor(t, fx.s, fx.key, "")
	out := feed(t, fx.s, fx.key, "", cur)
	if entryOf(out, "nod_st_file") != nil {
		t.Fatal("a staged node reached the account feed")
	}
	// Publish into the team folder.
	if err := fx.s.store.Write(ctx, func(tx *store.Tx) error {
		if err := tx.MoveStagedTree(ctx, fx.acc.ID, "fld_st", fx.fld.ID, store.Now()); err != nil {
			return err
		}
		return tx.DropStaging(ctx, fx.acc.ID, "fld_st", "nod_st_root")
	}); err != nil {
		t.Fatal(err)
	}
	out = feed(t, fx.s, fx.key, "", cur)
	if entryOf(out, "nod_st_file") == nil {
		t.Fatal("the published node never reached the account feed")
	}
}

// waitServer starts a real HTTP server over the test server's handler.
func waitServer(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// waitGET reads the feed over real HTTP with a context, returning the body, the elapsed time and the answer headers.
func waitGET(t *testing.T, srv *httptest.Server, cred string, q url.Values, ctx context.Context) (changesJSON, time.Duration, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/changes?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+cred)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("wait GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if resp.StatusCode != 200 {
		t.Fatalf("wait GET status %d: %s", resp.StatusCode, body)
	}
	var out changesJSON
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out, elapsed, resp.Header
}

// TestChangesWaitAnswersOnCommit: a held wait answers soon after a write commits, with the new entry.
func TestChangesWaitAnswersOnCommit(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	committed := make(chan time.Time, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		n := &store.Node{ID: "nod_wait", AccountID: fx.acc.ID, FolderID: fx.fld.ID, ParentID: fx.rootOf(t, fx.fld.ID),
			Name: "w.txt", NameKey: "w.txt", Kind: store.KindFile, Size: 1, ETag: "e", CreatedAt: store.Now(), ModifiedAt: store.Now()}
		if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
			return tx.InsertNodeNoIndex(context.Background(), n)
		}); err != nil {
			t.Error(err)
		}
		committed <- time.Now()
	}()
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"10"}}
	out, _, _ := waitGET(t, srv, fx.key, q, context.Background())
	at := <-committed
	if entryOf(out, "nod_wait") == nil {
		t.Fatalf("wait answer = %+v, want the new node", out)
	}
	if latency := time.Since(at); latency > time.Second {
		t.Errorf("the answer took %v after the commit, want under a second", latency)
	}
}

// TestChangesWaitTimesOut: a wait with no write answers empty at the deadline, the cursor unchanged.
func TestChangesWaitTimesOut(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"1"}}
	out, elapsed, _ := waitGET(t, srv, fx.key, q, context.Background())
	if elapsed < time.Second || elapsed > 3*time.Second {
		t.Errorf("wait elapsed %v, want about a second", elapsed)
	}
	if len(out.Changes) != 0 || out.Cursor != cur {
		t.Errorf("timeout answer = %+v, want empty with the same cursor", out)
	}
}

// TestChangesWaitEndsOnClientCancel: a client that cancels its request does not hold the wait to its deadline.
func TestChangesWaitEndsOnClientCancel(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"60"}}
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/changes?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+fx.key)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the cancelled wait held to its deadline")
	}
}

// heldWait starts a wait=60 request in the background and returns its cancel and a channel of its result.
func heldWait(t *testing.T, srv *httptest.Server, cred string, q url.Values) (context.CancelFunc, chan time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan time.Duration, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/changes?"+q.Encode(), nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		done <- time.Since(start)
	}()
	return cancel, done
}

// TestChangesWaitFifthCallerReturnsAtOnce: a caller holds at most four waits; the fifth answers at once.
func TestChangesWaitFifthCallerReturnsAtOnce(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"60"}}
	var cancels []context.CancelFunc
	var dones []chan time.Duration
	for i := 0; i < 4; i++ {
		c, d := heldWait(t, srv, fx.key, q)
		cancels = append(cancels, c)
		dones = append(dones, d)
	}
	time.Sleep(500 * time.Millisecond) // let the four register
	out, elapsed, hdr := waitGET(t, srv, fx.key, q, context.Background())
	if elapsed > time.Second {
		t.Errorf("the fifth wait took %v, want an immediate answer", elapsed)
	}
	if len(out.Changes) != 0 {
		t.Errorf("the fifth wait = %+v, want empty", out)
	}
	if hdr.Get("Retry-After") != "5" {
		t.Errorf("the refused wait carries Retry-After %q, want 5", hdr.Get("Retry-After"))
	}
	for _, c := range cancels {
		c()
	}
	for _, d := range dones {
		<-d
	}
}

// TestChangesWaitGlobalCap: with max_waiters 1, a second held wait answers at once.
func TestChangesWaitGlobalCap(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.s.cfg.Changes.MaxWaiters = 1
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"60"}}
	cancel, done := heldWait(t, srv, fx.key, q)
	time.Sleep(500 * time.Millisecond)
	_, elapsed, hdr := waitGET(t, srv, fx.key, q, context.Background())
	if elapsed > time.Second {
		t.Errorf("the over-cap wait took %v, want an immediate answer", elapsed)
	}
	if hdr.Get("Retry-After") != "5" {
		t.Errorf("the refused wait carries Retry-After %q, want 5", hdr.Get("Retry-After"))
	}
	cancel()
	<-done
}

// TestChangesWaitMaxWaitersZero: with max_waiters 0 every wait answers at once, with Retry-After 5.
func TestChangesWaitMaxWaitersZero(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.s.cfg.Changes.MaxWaiters = 0
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"60"}}
	out, elapsed, hdr := waitGET(t, srv, fx.key, q, context.Background())
	if elapsed > time.Second {
		t.Errorf("the wait took %v with max_waiters 0, want an immediate answer", elapsed)
	}
	if len(out.Changes) != 0 {
		t.Errorf("the wait = %+v, want empty", out)
	}
	if hdr.Get("Retry-After") != "5" {
		t.Errorf("the refused wait carries Retry-After %q, want 5", hdr.Get("Retry-After"))
	}
}

// TestChangesWaitFirstWakeAfterIdle: the 250 ms throttle is computed after the wake, so the first wake after a long
// idle answers at once, not a quarter second later.
func TestChangesWaitFirstWakeAfterIdle(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	committed := make(chan time.Time, 1)
	go func() {
		time.Sleep(600 * time.Millisecond) // idle well past the throttle
		n := &store.Node{ID: "nod_idle", AccountID: fx.acc.ID, FolderID: fx.fld.ID, ParentID: fx.rootOf(t, fx.fld.ID),
			Name: "i.txt", NameKey: "i.txt", Kind: store.KindFile, Size: 1, ETag: "e", CreatedAt: store.Now(), ModifiedAt: store.Now()}
		if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
			return tx.InsertNodeNoIndex(context.Background(), n)
		}); err != nil {
			t.Error(err)
		}
		committed <- time.Now()
	}()
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"10"}}
	out, _, _ := waitGET(t, srv, fx.key, q, context.Background())
	at := <-committed
	if entryOf(out, "nod_idle") == nil {
		t.Fatalf("wait answer = %+v, want the new node", out)
	}
	if latency := time.Since(at); latency > 200*time.Millisecond {
		t.Errorf("the first wake after the idle took %v to answer, want at once (under 200 ms)", latency)
	}
}

// TestChangesWaitThrottleNeverPastDeadline: a wake late in the wait never holds the answer past the deadline: the
// throttle sleep is capped at it. A write to another space wakes the wait without giving it rows.
func TestChangesWaitThrottleNeverPastDeadline(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Elsewhere")
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	stop := make(chan struct{})
	go func() {
		// One wake late in the wait, then silence: the answer must still come at the deadline.
		time.Sleep(1900 * time.Millisecond)
		n := &store.Node{ID: "nod_late", AccountID: fx.acc.ID, FolderID: other.ID, ParentID: fx.rootOf(t, other.ID),
			Name: "l.txt", NameKey: "l.txt", Kind: store.KindFile, Size: 1, ETag: "e", CreatedAt: store.Now(), ModifiedAt: store.Now()}
		if err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
			return tx.InsertNodeNoIndex(context.Background(), n)
		}); err != nil {
			t.Error(err)
		}
		close(stop)
	}()
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"2"}}
	out, elapsed, _ := waitGET(t, srv, fx.key, q, context.Background())
	<-stop
	if elapsed < 2*time.Second || elapsed > 2100*time.Millisecond {
		t.Errorf("wait elapsed %v, want the 2 s deadline, not past it", elapsed)
	}
	if len(out.Changes) != 0 || out.Cursor != cur {
		t.Errorf("deadline answer = %+v, want empty with the same cursor", out)
	}
}

// TestChangesWaitKeyIsTheUserBehindAToken: end users behind one integration key each hold their own four waits:
// two users' tokens hold eight at once, and each user's fifth answers at once while the other's waits stay held.
func TestChangesWaitKeyIsTheUserBehindAToken(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	root := fx.rootOf(t, fx.fld.ID)
	fx.setPerm(t, root, fx.ann.ID, "viewer")
	fx.setPerm(t, root, fx.bob.ID, "viewer")
	annTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.ann.ID})
	bobTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}, "user": fx.bob.ID})
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"60"}}
	var cancels []context.CancelFunc
	var dones []chan time.Duration
	for i := 0; i < 4; i++ {
		c, d := heldWait(t, srv, annTok, q)
		cancels = append(cancels, c)
		dones = append(dones, d)
		c, d = heldWait(t, srv, bobTok, q)
		cancels = append(cancels, c)
		dones = append(dones, d)
	}
	time.Sleep(500 * time.Millisecond) // let the eight register
	for _, tok := range []string{annTok, bobTok} {
		out, elapsed, hdr := waitGET(t, srv, tok, q, context.Background())
		if elapsed > time.Second || len(out.Changes) != 0 {
			t.Errorf("a user's fifth wait took %v with %+v, want an immediate empty answer", elapsed, out)
		}
		if hdr.Get("Retry-After") != "5" {
			t.Errorf("the refused wait carries Retry-After %q, want 5", hdr.Get("Retry-After"))
		}
	}
	for _, c := range cancels {
		c()
	}
	for i, d := range dones {
		if elapsed := <-d; elapsed < 400*time.Millisecond {
			t.Errorf("wait %d was refused after %v though its user held fewer than four", i, elapsed)
		}
	}
}

// TestChangesWaitKeyOfAnAccountKeyToken: a token acting as the account key keeps the key's own wait key: the bare
// key's four waits refuse the token's fifth.
func TestChangesWaitKeyOfAnAccountKeyToken(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	keyTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
	srv := waitServer(t, fx.s)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	q := url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"60"}}
	var cancels []context.CancelFunc
	var dones []chan time.Duration
	for i := 0; i < 4; i++ {
		c, d := heldWait(t, srv, fx.key, q)
		cancels = append(cancels, c)
		dones = append(dones, d)
	}
	time.Sleep(500 * time.Millisecond)
	_, elapsed, hdr := waitGET(t, srv, keyTok, q, context.Background())
	if elapsed > time.Second {
		t.Errorf("the token's wait took %v, want an immediate answer behind the key's four", elapsed)
	}
	if hdr.Get("Retry-After") != "5" {
		t.Errorf("the refused wait carries Retry-After %q, want 5", hdr.Get("Retry-After"))
	}
	for _, c := range cancels {
		c()
	}
	for _, d := range dones {
		<-d
	}
}

// TestChangesWaitEndsOnShutdown: a held wait ends when the server begins shutting down, not at the shutdown deadline.
func TestChangesWaitEndsOnShutdown(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.ShutdownTimeout = config.Duration(30 * time.Second) })
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Team")
	cur := startCursor(t, s, key, fld.ID)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, ln, nil) }()
	time.Sleep(200 * time.Millisecond) // let Serve start

	q := url.Values{"space": {fld.ID}, "cursor": {cur}, "wait": {"60"}}
	req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/api/v1/changes?"+q.Encode(), nil)
	req.Header.Set("Authorization", "Bearer "+key)
	answer := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		answer <- time.Since(start)
	}()
	time.Sleep(300 * time.Millisecond) // let the wait register
	cancel()                           // begin shutdown
	select {
	case elapsed := <-answer:
		if elapsed > 5*time.Second {
			t.Errorf("the wait held %v into shutdown, want it to end at once", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not end on shutdown")
	}
	select {
	case <-served:
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// TestChangesParams: limit and wait are bounded; out-of-range values are refused.
func TestChangesParams(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	cur := startCursor(t, fx.s, fx.key, fx.fld.ID)
	for _, limit := range []string{"0", "1001", "-1", "abc"} {
		rec := feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "limit": {limit}})
		wantError(t, rec, 400, apierr.InvalidRequest)
	}
	for _, wait := range []string{"-1", "61", "abc"} {
		rec := feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {wait}})
		wantError(t, rec, 400, apierr.InvalidRequest)
	}
	// The bounds themselves are accepted (with rows present so a wait answers at once).
	putNode(t, fx.s, fx.key, fx.fld.ID, "/bound.txt", "x")
	wantStatus(t, feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "limit": {"1000"}}), 200)
	wantStatus(t, feedCall(t, fx.s, fx.key, url.Values{"space": {fx.fld.ID}, "cursor": {cur}, "wait": {"0"}}), 200)
}

// TestChangesRateLimit: the changes limiter allows 120 calls a minute per caller and refuses the next.
func TestChangesRateLimit(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for i := 0; i < 120; i++ {
		rec := call(t, fx.s, "GET", "/api/v1/changes/start?space="+fx.fld.ID, fx.key, nil)
		if rec.Code != 200 {
			t.Fatalf("call %d: status %d, want 200: %s", i, rec.Code, rec.Body)
		}
	}
	rec := call(t, fx.s, "GET", "/api/v1/changes/start?space="+fx.fld.ID, fx.key, nil)
	wantError(t, rec, 429, apierr.RateLimited)
}
