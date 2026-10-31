// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// bulkDir makes a folder named bulk in folderID, with n files in it, in one transaction. The files are named
// bulk-file-NNNN.txt.
func bulkDir(t *testing.T, s *Server, accID, folderID string, n int) string {
	t.Helper()
	ctx := t.Context()
	dir := &store.Node{ID: id.New("nod"), AccountID: accID, FolderID: folderID, Name: "bulk", NameKey: "bulk",
		Kind: store.KindDir, CreatedAt: store.Now(), ModifiedAt: store.Now(), CreatedBy: "usr_bulk", ModifiedBy: "usr_bulk"}
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		root, err := tx.RootNode(ctx, accID, folderID)
		if err != nil {
			return err
		}
		dir.ParentID = root.ID
		if err := tx.InsertNode(ctx, dir); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("bulk-file-%04d.txt", i)
			f := &store.Node{ID: id.New("nod"), AccountID: accID, FolderID: folderID, ParentID: dir.ID, Name: name,
				NameKey: name, Kind: store.KindFile, Size: 1, BlobID: "blb_bulk", ETag: `"e"`, CreatedAt: store.Now(),
				ModifiedAt: store.Now(), CreatedBy: "usr_bulk", ModifiedBy: "usr_bulk"}
			if err := tx.InsertNode(ctx, f); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return dir.ID
}

// checkBulkEvents asserts the events of a change that touched files files: one per file at the threshold, and one
// folder summary above it, with no file named in it.
func checkBulkEvents(t *testing.T, evs []outboxRow, files int, eventType string) {
	t.Helper()
	if files == store.WebhookFolderThreshold {
		if len(evs) != files {
			t.Fatalf("%d files: %d events, want one per file", files, len(evs))
		}
		for _, e := range evs {
			if e.Type != eventType || strings.Contains(e.Payload, `"kind":"dir"`) {
				t.Fatalf("per-file event %+v", e)
			}
		}
		return
	}
	if len(evs) != 1 || evs[0].Type != eventType {
		t.Fatalf("%d files: events %+v, want one %s summary", files, evs, eventType)
	}
	if !strings.Contains(evs[0].Payload, `"kind":"dir"`) || !strings.Contains(evs[0].Payload, `"subtree":true`) {
		t.Fatalf("summary is not a folder with subtree true: %s", evs[0].Payload)
	}
	if strings.Contains(evs[0].Payload, "bulk-file-") {
		t.Fatalf("summary names a file: %s", evs[0].Payload)
	}
}

// A copy of a folder reports each file it makes at the threshold, and the folder once above it.
func TestWebhookCopyAtAndAboveThreshold(t *testing.T) {
	for _, files := range []int{store.WebhookFolderThreshold, store.WebhookFolderThreshold + 1} {
		fx := newWebhookFixture(t)
		fx.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.created"}})
		one := newFolder(t, fx.s, fx.key, fx.acc.ID, "One")
		dir := bulkDir(t, fx.s, fx.acc.ID, one.ID, files)

		seen := len(outboxOf(t, fx.s, fx.acc.ID))
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/nodes/"+dir+"/copy", fx.key, map[string]any{"name": "copied"}), 201)
		checkBulkEvents(t, outboxOf(t, fx.s, fx.acc.ID)[seen:], files, "file.created")
	}
}

// An ownership transfer into My files follows the same rule.
func TestWebhookOwnershipTransferAtAndAboveThreshold(t *testing.T) {
	for _, files := range []int{store.WebhookFolderThreshold, store.WebhookFolderThreshold + 1} {
		fx := newSharingFixture(t)
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/webhooks", fx.key,
			map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.moved"}}), 201)
		fx.personalFolder(t, fx.bobSess)
		annPF := fx.personalFolder(t, fx.annSess)
		dir := bulkDir(t, fx.s, fx.acc.ID, annPF, files)

		seen := len(outboxOf(t, fx.s, fx.acc.ID))
		wantStatus(t, callSession(t, fx.s, fx.annSess, "POST", "/api/v1/nodes/"+dir+"/transfer-ownership",
			map[string]any{"to": fx.bob.ID}), 200)
		checkBulkEvents(t, outboxOf(t, fx.s, fx.acc.ID)[seen:], files, "file.moved")
	}
}

// Deleting a user whose My files move to another user follows the same rule.
func TestWebhookDeletedUsersFilesAtAndAboveThreshold(t *testing.T) {
	for _, files := range []int{store.WebhookFolderThreshold, store.WebhookFolderThreshold + 1} {
		fx := newSharingFixture(t)
		wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/webhooks", fx.key,
			map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.moved"}}), 201)
		carl := newMember(t, fx, "carl@example.com")
		rec := call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/users", fx.key,
			map[string]any{"email": fmt.Sprintf("bulk%d@example.com", files), "verification": "none"})
		wantStatus(t, rec, 201)
		victim := decode[userResponse](t, rec)
		pf, err := fx.s.store.PersonalFolder(t.Context(), fx.acc.ID, victim.ID)
		if err != nil {
			t.Fatal(err)
		}
		bulkDir(t, fx.s, fx.acc.ID, pf.ID, files)

		seen := len(outboxOf(t, fx.s, fx.acc.ID))
		wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+victim.ID+
			"?transfer_to="+carl.user.ID, fx.key, nil), 204)
		checkBulkEvents(t, outboxOf(t, fx.s, fx.acc.ID)[seen:], files, "file.moved")
	}
}
