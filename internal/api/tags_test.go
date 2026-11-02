// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/mail"
)

// audioFixture reads one of the audio test files of internal/audio (ffmpeg made, one second of silence with tags).
func audioFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../audio/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// waitTags asks until the tags of a node are no longer being read, and returns that answer's body. It fails the test when
// no answer comes in time.
func waitTags(t *testing.T, s *Server, key, nodeID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		rec := rawCall(t, s, "GET", "/api/v1/nodes/"+nodeID+"/tags", key, "", nil)
		if rec.Code == http.StatusAccepted {
			if time.Now().After(deadline) {
				t.Fatalf("the tags of %s are still pending after 60 seconds", nodeID)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		wantStatus(t, rec, http.StatusOK)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
}

func TestAudioTagsAreReadServedAndListed(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Music")
	song := putImage(t, s, key, fld.ID, "/tag24.mp3", "audio/mpeg", audioFixture(t, "tag24.mp3"))

	got := waitTags(t, s, key, song.ID)
	if got["state"] != "ready" || got["title"] != "שלום עולם" || got["artist"] != "Artist One" || got["album"] != "Album X" {
		t.Fatalf("tags: %v", got)
	}
	if d, _ := got["duration_ms"].(float64); d < 1040 || d > 1050 {
		t.Fatalf("duration_ms %v, want about 1044", got["duration_ms"])
	}

	// The listing carries the values of the file once they are read, and the batch route answers the same.
	list := decode[struct {
		Items []nodeJSON `json:"items"`
	}](t, call(t, s, "GET", "/api/v1/folders/"+fld.ID+"/children", key, nil))
	if len(list.Items) != 1 || list.Items[0].Tags == nil || list.Items[0].Tags.Artist != "Artist One" {
		t.Fatalf("listing: %+v", list.Items)
	}
	batch := decode[struct {
		Items map[string]map[string]any `json:"items"`
	}](t, call(t, s, "GET", "/api/v1/media/tags?ids="+song.ID, key, nil))
	if batch.Items[song.ID]["state"] != "ready" || batch.Items[song.ID]["title"] != "שלום עולם" {
		t.Fatalf("batch: %v", batch.Items)
	}

	// The cover is the file's thumbnail: a JPEG, from the thumbnail route, like an image's.
	rec := waitThumb(t, s, key, nil, song.ID, 0)
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("cover content type %q", rec.Header().Get("Content-Type"))
	}
	imageSize(t, rec.Body.Bytes())
}

func TestAudioWithoutTagsOrCoverSaysSo(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Music")
	plain := putImage(t, s, key, fld.ID, "/plain.mp3", "audio/mpeg", audioFixture(t, "v1.mp3"))

	got := waitTags(t, s, key, plain.ID)
	if got["state"] != "ready" || got["title"] != nil {
		t.Fatalf("a file with only the frames has no title: %v", got)
	}
	// No picture: the thumbnail is refused as a file with no thumbnail, after the read fails for want of one.
	if rec := waitThumb(t, s, key, nil, plain.ID, 0); rec.Code != http.StatusNotFound {
		t.Fatalf("cover of a file without one: %d %s", rec.Code, rec.Body)
	}
}

func TestCorruptAudioIsNoneNotAnError(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Music")
	junk := []byte(strings.Repeat("not audio at all, just text ", 40))
	bad := putImage(t, s, key, fld.ID, "/bad.flac", "audio/flac", junk)

	if got := waitTags(t, s, key, bad.ID); got["state"] != "none" {
		t.Fatalf("a file the reader refuses: %v", got)
	}
}

func TestTagsAreNotGivenToAnotherAccount(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	_, other := newAccount(t, s, admin, "Other")
	fld := newFolder(t, s, key, acc.ID, "Music")
	song := putImage(t, s, key, fld.ID, "/tag.flac", "audio/flac", audioFixture(t, "tag.flac"))
	waitTags(t, s, key, song.ID)

	if rec := rawCall(t, s, "GET", "/api/v1/nodes/"+song.ID+"/tags", other, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("another account reads the tags: %d %s", rec.Code, rec.Body)
	}
	// The batch leaves the node out, which is the same answer as a node that does not exist.
	batch := decode[struct {
		Items map[string]any `json:"items"`
	}](t, call(t, s, "GET", "/api/v1/media/tags?ids="+song.ID, other, nil))
	if _, ok := batch.Items[song.ID]; ok {
		t.Fatalf("another account sees the node in the batch: %v", batch.Items)
	}
}

func TestTagsOfAFolderOrAnImageAreNotFound(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Music")
	pic := putImage(t, s, key, fld.ID, "/pic.png", "image/png", pngBytes(t, 4, 4))

	if rec := rawCall(t, s, "GET", "/api/v1/nodes/"+fld.ID+"/tags", key, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("a folder's tags: %d", rec.Code)
	}
	if rec := rawCall(t, s, "GET", "/api/v1/nodes/"+pic.ID+"/tags", key, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("an image's tags: %d", rec.Code)
	}
	batch := decode[struct {
		Items map[string]any `json:"items"`
	}](t, call(t, s, "GET", "/api/v1/media/tags?ids="+fld.ID+","+pic.ID, key, nil))
	if len(batch.Items) != 0 {
		t.Fatalf("a folder and an image in the batch: %v", batch.Items)
	}
}

func TestTagsBatchRefusesBadIDs(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	_, key := newAccount(t, s, admin, "Acme")
	// One more than the batch allows: the count is checked before any ID is read.
	tooMany := strings.Repeat("nod_x,", tagsBatch) + "nod_x"
	for _, q := range []string{"", "nod_x", "nod_a,nod_a", tooMany} {
		if rec := rawCall(t, s, "GET", "/api/v1/media/tags?ids="+q, key, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("ids=%.30q: %d, want 400", q, rec.Code)
		}
	}
}

// A batch whose budget of new reads runs out is answered in part: the file already read is ready, and the one not yet read
// is "pending" for the client to ask again. The batch is not refused with 429 (decision 56).
func TestTagsBatchAnswersInPartWhenTheBudgetRunsOut(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	runThumbnails(t, s)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fld := newFolder(t, s, key, acc.ID, "Music")
	read := putImage(t, s, key, fld.ID, "/tag.flac", "audio/flac", audioFixture(t, "tag.flac"))
	unread := putImage(t, s, key, fld.ID, "/tag24.mp3", "audio/mpeg", audioFixture(t, "tag24.mp3"))
	// One new read is allowed: the single route spends it on the first file, which then finishes.
	s.limits["tags.enqueue"] = newLimiter(1)
	waitTags(t, s, key, read.ID)

	rec := call(t, s, "GET", "/api/v1/media/tags?ids="+read.ID+","+unread.ID, key, nil)
	wantStatus(t, rec, http.StatusOK)
	batch := decode[struct {
		Items map[string]map[string]any `json:"items"`
	}](t, rec)
	if got := batch.Items[read.ID]["state"]; got != "ready" {
		t.Fatalf("the read file: %v, want ready", batch.Items)
	}
	if got := batch.Items[unread.ID]["state"]; got != "pending" {
		t.Fatalf("the unread file: %v, want pending", batch.Items)
	}
}

// The batch route is limited per caller by its requests, not only by the reads it queues: each request walks up to tagsBatch
// nodes for access, and a caller who repeats the requests is refused with 429 once the budget is spent (decision 56). A caller
// with a budget left is answered as before.
func TestTagsBatchRequestsAreLimitedPerCaller(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	_, key := newAccount(t, s, admin, "Acme")
	s.limits["tags.batch"] = newLimiter(2)
	// A node that does not exist: each request still costs its access walk, which is what the limit counts.
	path := "/api/v1/media/tags?ids=" + id.New("nod")
	for i := 0; i < 2; i++ {
		wantStatus(t, rawCall(t, s, "GET", path, key, "", nil), http.StatusOK)
	}
	wantStatus(t, rawCall(t, s, "GET", path, key, "", nil), http.StatusTooManyRequests)
}

// listedTags returns the tags a listing shows, by node ID. A listing that is refused shows none.
func listedTags(t *testing.T, rec *httptest.ResponseRecorder) map[string]*tagsJSON {
	t.Helper()
	out := map[string]*tagsJSON{}
	if rec.Code != http.StatusOK {
		return out
	}
	list := decode[struct {
		Items []nodeJSON `json:"items"`
	}](t, rec)
	for _, it := range list.Items {
		out[it.ID] = it.Tags
	}
	return out
}

// assertNoTagsFor fails when a listing or a batch shows the tags of a node the caller must not see.
func assertNoTagsFor(t *testing.T, listing map[string]*tagsJSON, nodeID string) {
	t.Helper()
	if tags, ok := listing[nodeID]; ok && tags != nil {
		t.Fatalf("the listing shows tags of %s: %+v", nodeID, tags)
	}
}

// A file the caller cannot reach, and one in the trash, get the same answer as one that is not there: 404 on the single
// route, no item in the batch, and no tags in the listing (decision 56).
func TestTagsAreNotGivenWhereTheFileIsUnreachableOrTrashed(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	runThumbnails(t, fx.s)
	song := putImage(t, fx.s, fx.key, fx.fld.ID, "/song.flac", "audio/flac", audioFixture(t, "tag.flac"))
	waitTags(t, fx.s, fx.key, song.ID)

	// ann has no role on the file; the file is in a folder she cannot see.
	single := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+song.ID+"/tags", nil)
	wantStatus(t, single, http.StatusNotFound)
	batch := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/media/tags?ids="+song.ID, nil)
	wantStatus(t, batch, http.StatusOK)
	if _, ok := decode[struct {
		Items map[string]any `json:"items"`
	}](t, batch).Items[song.ID]; ok {
		t.Fatal("the batch names an unreachable file")
	}
	assertNoTagsFor(t, listedTags(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/folders/"+fx.fld.ID+"/children", nil)), song.ID)

	// The owner trashes the file: the same answers for the owner, who could read it a moment before.
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+song.ID, fx.key, nil), http.StatusNoContent)
	wantStatus(t, call(t, fx.s, "GET", "/api/v1/nodes/"+song.ID+"/tags", fx.key, nil), http.StatusNotFound)
	batch = call(t, fx.s, "GET", "/api/v1/media/tags?ids="+song.ID, fx.key, nil)
	wantStatus(t, batch, http.StatusOK)
	if _, ok := decode[struct {
		Items map[string]any `json:"items"`
	}](t, batch).Items[song.ID]; ok {
		t.Fatal("the batch names a trashed file")
	}
	assertNoTagsFor(t, listedTags(t, call(t, fx.s, "GET", "/api/v1/folders/"+fx.fld.ID+"/children", fx.key, nil)), song.ID)
}

// A token scoped to another folder reaches none of this folder's audio: the same answers as for a file that is not there.
func TestTagsAreNotGivenThroughAnotherFoldersToken(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	runThumbnails(t, fx.s)
	song := putImage(t, fx.s, fx.key, fx.fld.ID, "/song.flac", "audio/flac", audioFixture(t, "tag.flac"))
	waitTags(t, fx.s, fx.key, song.ID)
	other := newFolder(t, fx.s, fx.key, fx.acc.ID, "Other")
	tok := mintToken(t, fx.s, fx.key, fx.acc.ID, other.ID, "")

	wantStatus(t, rawCall(t, fx.s, "GET", "/api/v1/nodes/"+song.ID+"/tags", tok, "", nil), http.StatusNotFound)
	batch := call(t, fx.s, "GET", "/api/v1/media/tags?ids="+song.ID, tok, nil)
	wantStatus(t, batch, http.StatusOK)
	if _, ok := decode[struct {
		Items map[string]any `json:"items"`
	}](t, batch).Items[song.ID]; ok {
		t.Fatal("the batch names a file of another folder")
	}
	assertNoTagsFor(t, listedTags(t, call(t, fx.s, "GET", "/api/v1/folders/"+fx.fld.ID+"/children", tok, nil)), song.ID)
}

// An anyone-link visitor is refused by the tags route and the batch (link_route, as for a thumbnail). The folder listing is
// refused to a file's link, so it shows no tags either; a folder-level link is not used here because it is not a node.
func TestTagsAreNotGivenToAnAnyoneVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	runThumbnails(t, fx.s)
	song := putImage(t, fx.s, fx.key, fx.fld.ID, "/song.flac", "audio/flac", audioFixture(t, "tag.flac"))
	waitTags(t, fx.s, fx.key, song.ID)
	_, secret := fx.makeAnyone(t, song.ID, nil)
	vis := fx.visitAnyone(t, secret, "")

	wantError(t, call(t, fx.s, "GET", "/api/v1/nodes/"+song.ID+"/tags", vis, nil), http.StatusForbidden, apierr.Forbidden)
	wantError(t, call(t, fx.s, "GET", "/api/v1/media/tags?ids="+song.ID, vis, nil), http.StatusForbidden, apierr.Forbidden)
	assertNoTagsFor(t, listedTags(t, call(t, fx.s, "GET", "/api/v1/folders/"+fx.fld.ID+"/children", vis, nil)), song.ID)
}

// A viewer whose file may not be downloaded still gets its tags on the route and in the listing: a tags read is viewing,
// not downloading. Whether covers and tags should be refused to such a viewer is for the owner to decide (decision 56).
func TestTagsAreGivenToANoDownloadViewer(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	runThumbnails(t, fx.s)
	song := putImage(t, fx.s, fx.key, fx.fld.ID, "/song.flac", "audio/flac", audioFixture(t, "tag.flac"))
	waitTags(t, fx.s, fx.key, song.ID)
	fx.setPerm(t, fx.rootOf(t, fx.fld.ID), fx.ann.ID, "viewer")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+song.ID, fx.key, map[string]any{"no_download": true}), 200)

	single := decode[map[string]any](t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/nodes/"+song.ID+"/tags", nil))
	if single["state"] != "ready" || single["title"] != "Flac Title" {
		t.Fatalf("the viewer's single answer: %v", single)
	}
	list := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/folders/"+fx.fld.ID+"/children", nil)
	if got := listedTags(t, list)[song.ID]; got == nil || got.Title != "Flac Title" {
		t.Fatalf("the viewer's listing shows %+v for the file", got)
	}
}

// An administrator who signs in as a member sees what the member sees: the batch and the single route refuse the file the
// member has no role on, and the listing shows no tags for it.
func TestTagsFollowAnImpersonationsReach(t *testing.T) {
	t.Parallel()
	fx := newImpFix(t, true)
	runThumbnails(t, fx.s)
	fld := newFolder(t, fx.s, fx.accKey, fx.acc.ID, "Music")
	mine := putImage(t, fx.s, fx.accKey, fld.ID, "/mine.flac", "audio/flac", audioFixture(t, "tag.flac"))
	private := putImage(t, fx.s, fx.accKey, fld.ID, "/private.mp3", "audio/mpeg", audioFixture(t, "tag24.mp3"))
	waitTags(t, fx.s, fx.accKey, mine.ID)
	waitTags(t, fx.s, fx.accKey, private.ID)
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+mine.ID+"/permissions/"+fx.member.ID, fx.accKey,
		map[string]any{"role": "viewer"}), 200)

	rec := fx.signInAs(t, fx.member.ID)
	wantStatus(t, rec, http.StatusOK)
	sess := sessionFrom(t, fx.s, rec)
	wantStatus(t, callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+mine.ID+"/tags", nil), http.StatusOK)
	wantStatus(t, callSession(t, fx.s, sess, "GET", "/api/v1/nodes/"+private.ID+"/tags", nil), http.StatusNotFound)
	batch := callSession(t, fx.s, sess, "GET", "/api/v1/media/tags?ids="+mine.ID+","+private.ID, nil)
	wantStatus(t, batch, http.StatusOK)
	items := decode[struct {
		Items map[string]map[string]any `json:"items"`
	}](t, batch).Items
	if items[mine.ID]["state"] != "ready" {
		t.Fatalf("the batch for the member's own file: %v", items)
	}
	if _, ok := items[private.ID]; ok {
		t.Fatal("the batch names the file the member cannot reach")
	}
	assertNoTagsFor(t, listedTags(t, callSession(t, fx.s, sess, "GET", "/api/v1/folders/"+fld.ID+"/children", nil)), private.ID)
}
