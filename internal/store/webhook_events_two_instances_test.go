// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Two instances on one database: an endpoint made through the second is seen by the first, which emits to it. The
// outbox asks the database in each transaction, so no instance keeps a copy of the endpoints that could go stale.
func TestEndpointMadeByAnotherInstanceReceivesEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "two.db")
	a, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	fx := newWebhookFixtureStore(t, a)
	ep := newEndpointState(t, b, fx.acc, "https://a.example.com/in", WebhookActive, "file.created")
	auditNode(t, a, fx.acc, fx.folder.ID, fx.root.ID, "two.txt", KindFile, "node.create", nil)

	if got := countRows(t, a, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", ep.ID); got != 1 {
		t.Fatalf("the first instance delivered %d events to an endpoint the second made, want 1", got)
	}
}
