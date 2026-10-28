// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// TestDecidedAccessRequestsArePurged: a decided request older than the cutoff is removed, and a pending one
// is kept however old it is, since someone still has to decide it.
func TestDecidedAccessRequestsArePurged(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	acc := newAccountRow(t, s, "A")
	ann := newUserRow(t, s, acc, "ann@example.com", "")
	bob := newUserRow(t, s, acc, "bob@example.com", "")
	f := newFolderRow(t, s, acc, "One", "one")
	root, err := s.RootNode(ctx, acc, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour)
	decided := &AccessRequest{ID: id.New("arq"), AccountID: acc, NodeID: root.ID, UserID: ann.ID,
		Role: RoleViewer, Status: RequestPending, CreatedAt: old}
	pending := &AccessRequest{ID: id.New("arq"), AccountID: acc, NodeID: root.ID, UserID: bob.ID,
		Role: RoleViewer, Status: RequestPending, CreatedAt: old}
	if err := s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.InsertAccessRequest(ctx, decided); err != nil {
			return err
		}
		if _, err := tx.InsertAccessRequest(ctx, pending); err != nil {
			return err
		}
		decided.Status = RequestDenied
		return tx.DecideAccessRequest(ctx, decided, "x", old)
	}); err != nil {
		t.Fatal(err)
	}
	var removed int64
	if err := s.Write(ctx, func(tx *Tx) error {
		var err error
		removed, err = tx.DeleteDecidedAccessRequests(ctx, time.Now().Add(-30*24*time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("purged %d requests, want 1", removed)
	}
	left, _, err := s.PendingAccessRequests(ctx, acc, Page{Limit: 10})
	if err != nil || len(left) != 1 || left[0].ID != pending.ID {
		t.Errorf("pending requests after the purge = %+v, %v; want the pending one only", left, err)
	}
}
