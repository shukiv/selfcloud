// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// A mapped audit entry whose node is missing fails its change, so the change never commits without its event.
func TestMappedAuditWithoutItsNodeFailsTheChange(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created")

	err := s.Write(context.Background(), func(tx *Tx) error {
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.create", Target: "nod_missing", Folders: []string{fx.folder.ID}})
	})
	if err == nil {
		t.Fatal("a mapped audit entry with no node was written without error")
	}
	if got := len(eventsOf(t, s, fx.acc)); got != 0 {
		t.Fatalf("%d events for a failed change, want 0", got)
	}
}
