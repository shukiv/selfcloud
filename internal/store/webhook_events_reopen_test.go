// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"path/filepath"
	"testing"
)

// An endpoint made before a restart still gets the events of the next start. The fan-out reads the endpoints inside each
// transaction, so nothing is loaded at Open.
func TestEndpointSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reopen.db")
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	fx := newWebhookFixtureStore(t, s)
	ep := newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.created")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	var root *Node
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		root, err = tx.RootNode(ctx, fx.acc, fx.folder.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	auditNode(t, s, fx.acc, fx.folder.ID, root.ID, "after-restart.txt", KindFile, "node.create", nil)

	if got := countRows(t, s, "SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id = ?", ep.ID); got != 1 {
		t.Fatalf("the endpoint of before the restart got %d deliveries, want 1", got)
	}
}
