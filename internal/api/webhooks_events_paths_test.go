// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// eventTypesSince lists the types of the events written after the first seen of the outbox.
func eventTypesSince(t *testing.T, s *Server, accountID string, seen int) []string {
	t.Helper()
	var out []string
	for _, r := range outboxOf(t, s, accountID)[seen:] {
		out = append(out, r.Type)
	}
	return out
}

// Restoring an older version reports the file as updated.
func TestWebhookVersionRestoreReportsUpdate(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.updated"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	a := putNode(t, fx.s, fx.key, one.ID, "/v.txt", "v1")
	wantStatus(t, rawCall(t, fx.s, "PUT", "/api/v1/nodes/"+a.ID+"/content", fx.key, "v2",
		map[string]string{"If-Match": a.ETag}), 200)

	list := decodeMap(t, call(t, fx.s, "GET", "/api/v1/nodes/"+a.ID+"/versions", fx.key, nil))
	items, _ := list["items"].([]any)
	var ver string
	for _, it := range items {
		if m, _ := it.(map[string]any); m["current"] == false {
			ver, _ = m["id"].(string)
		}
	}
	if ver == "" {
		t.Fatalf("the overwrite kept no older version: %v", list)
	}

	current := decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+a.ID, fx.key, nil))
	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, rawCall(t, fx.s, "POST", "/api/v1/nodes/"+a.ID+"/versions/"+ver+"/restore", fx.key, "",
		map[string]string{"If-Match": current.ETag}), 200)
	if got := eventTypesSince(t, fx.s, fx.acc.ID, seen); strings.Join(got, ",") != "file.updated" {
		t.Fatalf("restore events %v, want file.updated", got)
	}
}

// An upload that replaces a file reports the file as updated, then the upload as completed.
func TestWebhookUploadReplaceReportsUpdateThenCompletion(t *testing.T) {
	s, key, accID, f := uploadSetup(t)
	wantStatus(t, call(t, s, "POST", "/api/v1/accounts/"+accID+"/webhooks", key,
		map[string]any{"url": "https://hooks.example.com/in",
			"events": []string{"file.created", "file.updated", "upload.completed"}}), 201)

	first := "first body"
	upl := uploadID(t, createUpload(t, s, key, int64(len(first)), md("folder", f.ID, "name", "rep.txt", "sha256", digest(first))))
	wantStatus(t, piece(t, s, key, upl, 0, first, nil), 204)

	seen := len(outboxOf(t, s, accID))
	second := "second body"
	upl = uploadID(t, createUpload(t, s, key, int64(len(second)),
		md("folder", f.ID, "name", "rep.txt", "on_conflict", "replace", "sha256", digest(second))))
	wantStatus(t, piece(t, s, key, upl, 0, second, nil), 204)
	if got := eventTypesSince(t, s, accID, seen); strings.Join(got, ",") != "file.updated,upload.completed" {
		t.Fatalf("replacing upload events %v, want file.updated then upload.completed", got)
	}
}

// A copy that replaces a file reports the copy's file, then the displaced file as deleted.
func TestWebhookReplaceOnCopyReportsDisplacedFile(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created", "file.deleted"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	src := putNode(t, fx.s, fx.key, one.ID, "/src.txt", "source")
	putNode(t, fx.s, fx.key, one.ID, "/dst.txt", "displaced")

	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+src.ID+"/copy", fx.key,
		map[string]any{"name": "dst.txt", "on_conflict": "replace"}), 201)
	if got := eventTypesSince(t, fx.s, fx.acc.ID, seen); strings.Join(got, ",") != "file.created,file.deleted" {
		t.Fatalf("replace-on-copy events %v, want file.created then file.deleted", got)
	}
}

// A move that replaces a file reports the displaced file as deleted, then the moved file: the move audits the
// displacement first, as it happens.
func TestWebhookReplaceOnMoveReportsDisplacedFile(t *testing.T) {
	fx := newWebhookFixture(t)
	fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.moved", "file.deleted"}})
	one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
	mover := putNode(t, fx.s, fx.key, one.ID, "/mover.txt", "moving")
	putNode(t, fx.s, fx.key, one.ID, "/target.txt", "displaced")

	seen := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+mover.ID+"/move", fx.key,
		map[string]any{"name": "target.txt", "on_conflict": "replace"}), 200)
	if got := eventTypesSince(t, fx.s, fx.acc.ID, seen); strings.Join(got, ",") != "file.deleted,file.moved" {
		t.Fatalf("replace-on-move events %v, want file.deleted then file.moved", got)
	}
}
