// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"
)

// Deleting a user whose files move to another user reports each file that moved, and no folder.
func TestWebhookDeletedUsersFilesReportMoves(t *testing.T) {
	fx := newSharingFixture(t)
	wantStatus(t, call(t, fx.s, "POST", "/api/v1/accounts/"+fx.acc.ID+"/webhooks", fx.key,
		map[string]any{"url": "https://hooks.example.com/in", "events": []string{"file.moved"}}), 201)
	carl := newMember(t, fx, "carl@example.com")
	victim := victimWithFiles(t, fx, "victim@example.com")
	files := countOf(t, fx.s, `SELECT COUNT(*) FROM nodes n JOIN folders f ON f.id = n.folder_id
		WHERE n.account_id = ? AND n.kind = 'file' AND f.kind = 'personal' AND f.owner_user_id = ?`, fx.acc.ID, victim.user.ID)
	if files == 0 {
		t.Fatal("the fixture gave the victim no files")
	}

	before := len(outboxOf(t, fx.s, fx.acc.ID))
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+victim.user.ID+
		"?transfer_to="+carl.user.ID, fx.key, nil), 204)

	moved := outboxOf(t, fx.s, fx.acc.ID)[before:]
	if len(moved) != files {
		t.Fatalf("the move reported %d events for %d files", len(moved), files)
	}
	for _, e := range moved {
		if e.Type != "file.moved" || !strings.Contains(e.Payload, `"folder_id":"`) || strings.Contains(e.Payload, `"kind":"dir"`) {
			t.Fatalf("move event %+v", e)
		}
	}
}
