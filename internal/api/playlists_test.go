// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// plCreate makes a playlist of ann's (or bob's) and returns its ID.
func plCreate(t *testing.T, fx *sharingFixture, sess testSession, name string) string {
	t.Helper()
	rec := callSession(t, fx.s, sess, "POST", "/api/v1/playlists", map[string]any{"name": name})
	wantStatus(t, rec, 201)
	return decode[playlistJSON](t, rec).ID
}

// plAdd adds a node to a playlist as the session's user and expects the status.
func plAdd(t *testing.T, fx *sharingFixture, sess testSession, pls, node string, status int) {
	t.Helper()
	wantStatus(t, callSession(t, fx.s, sess, "POST", "/api/v1/playlists/"+pls+"/entries",
		map[string]any{"node_id": node}), status)
}

// plGet reads a playlist with its entries.
func plGet(t *testing.T, fx *sharingFixture, sess testSession, pls string) playlistDetailJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "GET", "/api/v1/playlists/"+pls, nil)
	wantStatus(t, rec, 200)
	return decode[playlistDetailJSON](t, rec)
}

// plEntry returns the entry of a node in a read playlist.
func plEntry(t *testing.T, pl playlistDetailJSON, node string) playlistEntryJSON {
	t.Helper()
	for _, e := range pl.Entries {
		if e.NodeID == node {
			return e
		}
	}
	t.Fatalf("no entry for node %s in %+v", node, pl)
	return playlistEntryJSON{}
}

// unavailableShape is what an unavailable entry says about its file, with the node ID left out (each entry names its own
// node, which differs by design). Two causes that answer alike give the same shape.
func unavailableShape(t *testing.T, e playlistEntryJSON) string {
	t.Helper()
	if e.Node != nil || !e.Unavailable {
		t.Fatalf("entry %+v is not unavailable, or carries a node", e)
	}
	b, err := json.Marshal(map[string]any{"unavailable": e.Unavailable})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sameAnswer compares two error answers, leaving out the request ID, which is new for each.
func sameAnswer(a, b string) bool {
	return requestIDRe.ReplaceAllString(a, "") == requestIDRe.ReplaceAllString(b, "")
}

var requestIDRe = regexp.MustCompile(`"request_id":"[^"]*"`)

// TestPlaylistCRUD: create, list, rename and delete, with the name bounds refused by code.
func TestPlaylistCRUD(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	pls := plCreate(t, fx, fx.annSess, "Evening")

	list := decode[playlistListJSON](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/playlists", nil))
	if len(list.Items) != 1 || list.Items[0].ID != pls || list.Items[0].Name != "Evening" || list.Items[0].EntryCount != 0 {
		t.Fatalf("list = %+v", list)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PATCH", "/api/v1/playlists/"+pls, map[string]any{"name": "Morning"}), 200)
	if got := plGet(t, fx, fx.annSess, pls); got.Name != "Morning" {
		t.Fatalf("after rename: %q", got.Name)
	}
	// Bounds: empty, too long by one character, and a control character are refused.
	for _, bad := range []string{"", "   ", strings.Repeat("x", maxPlaylistNameRunes+1), "a\nb"} {
		wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists", map[string]any{"name": bad}),
			400, apierr.InvalidRequest)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists",
		map[string]any{"name": strings.Repeat("é", maxPlaylistNameRunes)}), 201)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/playlists/"+pls, nil), 204)
	wantError(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/playlists/"+pls, nil), 404, apierr.NotFound)
}

// TestPlaylistIsPrivate: another user, even one who reaches the same file, sees neither the playlist nor its entries,
// and a playlist that does not exist answers the same.
func TestPlaylistIsPrivate(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Ann's")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)

	other := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/playlists/"+pls, nil)
	missing := callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/playlists/"+id.New("pls"), nil)
	wantError(t, other, 404, apierr.NotFound)
	if !sameAnswer(other.Body.String(), missing.Body.String()) {
		t.Errorf("another user's playlist and a missing one answer differently:\n%s\n%s", other.Body.String(), missing.Body.String())
	}
	if strings.Contains(other.Body.String(), "Ann's") || strings.Contains(other.Body.String(), fx.deep.ID) {
		t.Errorf("another user's playlist leaks its name or entries: %s", other.Body.String())
	}
	wantError(t, callSession(t, fx.s, fx.bobSess, "DELETE", "/api/v1/playlists/"+pls, nil), 404, apierr.NotFound)
	wantError(t, callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/playlists/"+pls+"/entries",
		map[string]any{"node_id": fx.deep.ID}), 404, apierr.NotFound)
	// Bob's list is his own, and empty.
	if list := decode[playlistListJSON](t, callSession(t, fx.s, fx.bobSess, "GET", "/api/v1/playlists", nil)); len(list.Items) != 0 {
		t.Errorf("bob lists %+v", list.Items)
	}
}

// TestPlaylistEntryWithAccess: a reachable file shows with its node and the caller's role; a file with no download
// shows as playable, as the queue plays it (x1's reading, decision 45).
func TestPlaylistEntryWithAccess(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	e := plEntry(t, plGet(t, fx, fx.annSess, pls), fx.deep.ID)
	if e.Unavailable || e.Node == nil || e.Node.Name != "file.txt" || e.Node.Role != "viewer" {
		t.Fatalf("reachable entry = %+v", e)
	}
	// A no-download file is listed as playable.
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.deep.ID, fx.key, map[string]any{"no_download": true}), 200)
	if e := plEntry(t, plGet(t, fx, fx.annSess, pls), fx.deep.ID); e.Unavailable || e.Node == nil {
		t.Fatalf("no-download entry = %+v, want playable", e)
	}
}

// TestPlaylistAccessRemoved: an entry whose access is removed reads as unavailable, with no name, size, type or role,
// and its shape is the same as a node that does not exist.
func TestPlaylistAccessRemoved(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)

	got := plGet(t, fx, fx.annSess, pls)
	e := plEntry(t, got, fx.deep.ID)
	if strings.Contains(callSessionBody(t, fx, fx.annSess, pls), "file.txt") {
		t.Fatal("an entry whose access was removed still shows its name")
	}
	// The same shape as an entry whose node does not exist at all.
	ghost := id.New("nod")
	insertEntry(t, fx, pls, ghost)
	g := plEntry(t, plGet(t, fx, fx.annSess, pls), ghost)
	if unavailableShape(t, e) != unavailableShape(t, g) {
		t.Errorf("revoked and missing entries differ: %s vs %s", unavailableShape(t, e), unavailableShape(t, g))
	}
}

// callSessionBody reads a playlist's raw body.
func callSessionBody(t *testing.T, fx *sharingFixture, sess testSession, pls string) string {
	t.Helper()
	return callSession(t, fx.s, sess, "GET", "/api/v1/playlists/"+pls, nil).Body.String()
}

// insertEntry writes an entry straight into the store, for a node that may not exist: the way an entry outlives its node.
func insertEntry(t *testing.T, fx *sharingFixture, pls, node string) {
	t.Helper()
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.AppendPlaylistEntry(context.Background(), store.PlaylistEntry{ID: id.New("ple"), PlaylistID: pls, NodeID: node}, store.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPlaylistTrashedAndDeletedEntries: a trashed file, and a file purged from the database, both read as unavailable,
// alike; and the unavailable entry can still be removed, and does not stop the list from reading.
func TestPlaylistTrashedAndDeletedEntries(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	plAdd(t, fx, fx.annSess, pls, fx.sib.ID, 404) // not reached: refused, not_found

	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.deep.ID, fx.key, nil), 204) // trashed
	trashed := plEntry(t, plGet(t, fx, fx.annSess, pls), fx.deep.ID)
	if !trashed.Unavailable || trashed.Node != nil {
		t.Fatalf("trashed entry = %+v", trashed)
	}

	// Purged from the database: the entry's node row is gone.
	purged := fx.sib.ID
	fx.setPerm(t, fx.c.ID, fx.ann.ID, "viewer")
	plAdd(t, fx, fx.annSess, pls, purged, 204)
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Querier().ExecContext(context.Background(), "DELETE FROM nodes WHERE id = ?", purged)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	gone := plEntry(t, plGet(t, fx, fx.annSess, pls), purged)
	if unavailableShape(t, trashed) != unavailableShape(t, gone) {
		t.Errorf("trashed and deleted entries differ: %s vs %s", unavailableShape(t, trashed), unavailableShape(t, gone))
	}
	// The unavailable entry is removed by its own ID; the list still reads.
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/playlists/"+pls+"/entries/"+gone.EntryID, nil), 204)
	if n := len(plGet(t, fx, fx.annSess, pls).Entries); n != 1 {
		t.Errorf("entries after removing one = %d, want 1", n)
	}
}

// TestPlaylistAnotherAccountIsNotFound: a node of another account is refused as a node that does not exist, and an entry
// of another account reads as unavailable.
func TestPlaylistAnotherAccountIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	other, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	ofld := newFolder(t, fx.s, otherKey, other.ID, "Theirs")
	foreign := putNode(t, fx.s, otherKey, ofld.ID, "/secret.txt", "secret")

	pls := plCreate(t, fx, fx.annSess, "Mine")
	missing := id.New("nod")
	plAdd(t, fx, fx.annSess, pls, foreign.ID, 404)
	plAdd(t, fx, fx.annSess, pls, missing, 404)
	a := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists/"+pls+"/entries", map[string]any{"node_id": foreign.ID})
	b := callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists/"+pls+"/entries", map[string]any{"node_id": missing})
	if !sameAnswer(a.Body.String(), b.Body.String()) {
		t.Errorf("foreign and missing node answer differently:\n%s\n%s", a.Body.String(), b.Body.String())
	}
	// An entry of another account, written in directly, reads unavailable.
	insertEntry(t, fx, pls, foreign.ID)
	e := plEntry(t, plGet(t, fx, fx.annSess, pls), foreign.ID)
	if !e.Unavailable || e.Node != nil || strings.Contains(callSessionBody(t, fx, fx.annSess, pls), "secret") {
		t.Errorf("a foreign entry reads %+v", e)
	}
}

// TestPlaylistUnreachableAddIsIndistinguishable: an add refused for an unreachable file and for a missing one gives the
// same answer, body and all, and an add of a folder is refused the same way.
func TestPlaylistUnreachableAddIsIndistinguishable(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.bob.ID, "viewer")
	pls := plCreate(t, fx, fx.bobSess, "Bob's")
	unreachable := callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/playlists/"+pls+"/entries", map[string]any{"node_id": fx.sib.ID})
	missing := callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/playlists/"+pls+"/entries", map[string]any{"node_id": id.New("nod")})
	folder := callSession(t, fx.s, fx.bobSess, "POST", "/api/v1/playlists/"+pls+"/entries", map[string]any{"node_id": fx.a.ID})
	if unreachable.Code != 404 || !sameAnswer(unreachable.Body.String(), missing.Body.String()) ||
		!sameAnswer(folder.Body.String(), missing.Body.String()) {
		t.Errorf("unreachable %d %s\nmissing %d %s\nfolder %d %s", unreachable.Code, unreachable.Body.String(),
			missing.Code, missing.Body.String(), folder.Code, folder.Body.String())
	}
}

// TestPlaylistReplaceKeepsUnavailableEntries: a replace refuses a new unreachable file, but keeps an entry the playlist
// already holds even when it is unavailable now, so the user can reorder and remove again.
func TestPlaylistReplaceKeepsUnavailableEntries(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+fx.b.ID+"/permissions/"+fx.ann.ID, fx.key, nil), 204)

	put := func(nodes ...string) *httptest.ResponseRecorder {
		return callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/playlists/"+pls+"/entries", map[string]any{"nodes": nodes})
	}
	wantError(t, put(fx.deep.ID, fx.sib.ID), 404, apierr.NotFound) // sib is new and not reached: refused as a whole
	wantStatus(t, put(fx.deep.ID), 204)                            // the unavailable entry is kept
	if e := plEntry(t, plGet(t, fx, fx.annSess, pls), fx.deep.ID); !e.Unavailable {
		t.Fatalf("kept entry = %+v", e)
	}
	wantError(t, put(fx.deep.ID, fx.deep.ID), 400, apierr.InvalidRequest) // duplicates are refused
	big := make([]string, maxPlaylistEntries+1)
	for i := range big {
		big[i] = id.New("nod")
	}
	wantError(t, put(big...), 400, apierr.InvalidRequest)
}

// TestPlaylistReorderAndLimits: reorder takes exactly the playlist's entries, once each; the entry bound and the
// playlist bound are refused by code.
func TestPlaylistReorderAndLimits(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	plAdd(t, fx, fx.annSess, pls, fx.sib.ID, 404)
	first := plEntry(t, plGet(t, fx, fx.annSess, pls), fx.deep.ID).EntryID
	insertEntry(t, fx, pls, id.New("nod"))
	got := plGet(t, fx, fx.annSess, pls)
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %d", len(got.Entries))
	}
	second := got.Entries[1].EntryID
	wantError(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/playlists/"+pls+"/order",
		map[string]any{"entries": []string{first}}), 400, apierr.InvalidRequest)
	wantError(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/playlists/"+pls+"/order",
		map[string]any{"entries": []string{first, first}}), 400, apierr.InvalidRequest)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/playlists/"+pls+"/order",
		map[string]any{"entries": []string{second, first}}), 204)
	if after := plGet(t, fx, fx.annSess, pls); after.Entries[0].EntryID != second || after.Entries[1].EntryID != first {
		t.Errorf("order after reorder = %+v", after.Entries)
	}

	// The playlist bound: a user holds at most maxPlaylistsPerUser.
	// Filled in the store: the route's own rate limit is not the subject here.
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		for i := len(plGetAll(t, fx)); i < maxPlaylistsPerUser; i++ {
			if err := tx.InsertPlaylist(context.Background(), store.Playlist{ID: id.New("pls"), AccountID: fx.acc.ID,
				UserID: fx.ann.ID, Name: "p"}, store.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists", map[string]any{"name": "one too many"}),
		409, apierr.Conflict)
}

// plGetAll lists the caller's playlists.
func plGetAll(t *testing.T, fx *sharingFixture) []playlistJSON {
	t.Helper()
	return decode[playlistListJSON](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/playlists", nil)).Items
}

// TestPlaylistTokensAndKeysHaveNoPart: a delegated token, even one that acts for the user with write, and the account
// key, are refused on every playlist route.
func TestPlaylistTokensAndKeysHaveNoPart(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID}, "permissions": "write"})
	wantError(t, call(t, fx.s, "GET", "/api/v1/playlists", tok, nil), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "GET", "/api/v1/playlists/"+pls, tok, nil), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "POST", "/api/v1/playlists", tok, map[string]any{"name": "x"}), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "DELETE", "/api/v1/playlists/"+pls, tok, nil), 403, apierr.Forbidden)
	wantError(t, call(t, fx.s, "GET", "/api/v1/playlists", fx.key, nil), 403, apierr.Forbidden)
	// The owner's own list is still there for the user.
	if n := len(plGetAll(t, fx)); n != 1 {
		t.Errorf("user lists %d playlists", n)
	}
}

// TestPlaylistAuditIsPrivate: each change writes an audit entry private to its user, with counts and no name.
func TestPlaylistAuditIsPrivate(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Secret name")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	wantStatus(t, callSession(t, fx.s, fx.annSess, "DELETE", "/api/v1/playlists/"+pls, nil), 204)

	rows := auditRows(t, fx, pls)
	want := []string{"playlist.create", "playlist.entries", "playlist.delete"}
	if strings.Join(rows.actions, ",") != strings.Join(want, ",") {
		t.Fatalf("audit actions = %v, want %v", rows.actions, want)
	}
	for _, p := range rows.private {
		if p != fx.ann.ID {
			t.Errorf("audit entry private to %q, want ann", p)
		}
	}
	if strings.Contains(rows.details, "Secret name") || strings.Contains(rows.details, fx.deep.ID) {
		t.Errorf("audit details name the playlist or a node: %s", rows.details)
	}
}

type auditRowSet struct {
	actions []string
	private []string
	details string
}

// auditRows reads the audit rows about one playlist.
func auditRows(t *testing.T, fx *sharingFixture, pls string) auditRowSet {
	t.Helper()
	var out auditRowSet
	err := fx.s.store.Read(context.Background(), func(q store.Querier) error {
		rows, err := q.QueryContext(context.Background(), `SELECT action, COALESCE(private_user_id, ''), COALESCE(details, '')
			FROM audit_log WHERE target = ? ORDER BY time, rowid`, pls)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a, p, d string
			if err := rows.Scan(&a, &p, &d); err != nil {
				return err
			}
			out.actions = append(out.actions, a)
			out.private = append(out.private, p)
			out.details += d
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPlaylistCreateWithEntriesIsAtomic: a playlist created with its files either gets all of them or nothing. One file the
// caller cannot reach refuses the whole create and leaves no playlist behind, so a retry cannot make a second one.
func TestPlaylistCreateWithEntriesIsAtomic(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists",
		map[string]any{"name": "Half", "nodes": []string{fx.deep.ID, fx.sib.ID}}), 404, apierr.NotFound)
	if n := len(plGetAll(t, fx)); n != 0 {
		t.Fatalf("a refused create left %d playlists", n)
	}
	wantError(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists",
		map[string]any{"name": "Twice", "nodes": []string{fx.deep.ID, fx.deep.ID}}), 400, apierr.InvalidRequest)
	if n := len(plGetAll(t, fx)); n != 0 {
		t.Fatalf("a refused create left %d playlists", n)
	}
	got := decode[playlistJSON](t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/playlists",
		map[string]any{"name": "Whole", "nodes": []string{fx.deep.ID}}))
	if got.EntryCount != 1 {
		t.Fatalf("created playlist holds %d entries, want 1", got.EntryCount)
	}
	if n := len(plGetAll(t, fx)); n != 1 {
		t.Fatalf("lists %d playlists after one create, want 1", n)
	}
}

// TestPlaylistAccountDeleteCascades: deleting the account removes its playlists and their entries; the user's
// removal does the same for that user's playlists.
func TestPlaylistAccountDeleteCascades(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Mine")
	plAdd(t, fx, fx.annSess, pls, fx.deep.ID, 204)
	err := fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		return tx.DeleteAccount(context.Background(), fx.acc.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	err = fx.s.store.Read(context.Background(), func(q store.Querier) error {
		return q.QueryRowContext(context.Background(),
			`SELECT (SELECT COUNT(*) FROM playlists) + (SELECT COUNT(*) FROM playlist_entries)`).Scan(&n)
	})
	if err != nil || n != 0 {
		t.Fatalf("rows left after account delete: %d, %v", n, err)
	}
}

// TestPlaylistReadCost measures a read of the largest playlist: 500 entries, each a file in a deep tree, read by a user
// with a reach on the top folder. The bound is generous; the number is logged for the decision.
func TestPlaylistReadCost(t *testing.T) {
	if testing.Short() {
		t.Skip("measures a large playlist")
	}
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.a.ID, fx.ann.ID, "viewer")
	pls := plCreate(t, fx, fx.annSess, "Largest")
	nodes := make([]string, 0, maxPlaylistEntries)
	for i := 0; i < maxPlaylistEntries; i++ {
		f := putNode(t, fx.s, fx.key, fx.fld.ID, "/a/b/track"+strconv.Itoa(i)+".mp3", "t")
		nodes = append(nodes, f.ID)
	}
	wantStatus(t, callSession(t, fx.s, fx.annSess, "PUT", "/api/v1/playlists/"+pls+"/entries", map[string]any{"nodes": nodes}), 204)
	start := time.Now()
	got := plGet(t, fx, fx.annSess, pls)
	took := time.Since(start)
	if len(got.Entries) != maxPlaylistEntries || got.Entries[0].Unavailable {
		t.Fatalf("read %d entries, first unavailable=%v", len(got.Entries), got.Entries[0].Unavailable)
	}
	t.Logf("GET of a %d-entry playlist (reach of each entry, in-process): %s", maxPlaylistEntries, took)
	if took > 5*time.Second {
		t.Errorf("GET of the largest playlist took %s, over the 5 s bound", took)
	}
}
