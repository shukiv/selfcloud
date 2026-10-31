// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// bulkFolder inserts a folder named bulk under the fixture's root, with n files in it, in one transaction. The files
// are named bulk-file-NNNN.txt, so a payload that names one of them is easy to see.
func bulkFolder(t *testing.T, s *Store, fx webhookFixtureStore, n int) *Node {
	t.Helper()
	ctx := context.Background()
	dir := &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.folder.ID, ParentID: fx.root.ID, Name: "bulk",
		NameKey: "bulk", Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNode(ctx, dir); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("bulk-file-%04d.txt", i)
			f := &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.folder.ID, ParentID: dir.ID, Name: name,
				NameKey: name, Kind: KindFile, Size: 1, BlobID: "blb_test", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now(),
				CreatedBy: "usr_test", ModifiedBy: "usr_test"}
			if err := tx.InsertNode(ctx, f); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// checkSummary asserts that the events are one folder summary of the given type: kind dir, subtree true, and no
// file named in it.
func checkSummary(t *testing.T, evs []eventRow, eventType string) {
	t.Helper()
	if len(evs) != 1 || evs[0].Type != eventType {
		t.Fatalf("events %d, want one %s summary", len(evs), eventType)
	}
	p := evs[0].Payload
	if !strings.Contains(p, `"kind":"dir"`) || !strings.Contains(p, `"subtree":true`) {
		t.Fatalf("summary is not a folder with subtree true: %s", p)
	}
	if strings.Contains(p, "bulk-file-") {
		t.Fatalf("summary names a file: %s", p)
	}
}

// trashDir trashes dir in one transaction, auditing it as a handler does.
func trashDir(t *testing.T, s *Store, fx webhookFixtureStore, dir *Node) {
	t.Helper()
	if err := s.Write(context.Background(), func(tx *Tx) error {
		if err := tx.TrashNode(context.Background(), fx.acc, dir.ID, "usr_test", Now()); err != nil {
			return err
		}
		return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
			ActorID: "key_test", Action: "node.trash", Target: dir.ID, Folders: []string{fx.folder.ID}})
	}); err != nil {
		t.Fatal(err)
	}
}

// At exactly the threshold, a folder's trash reports each file; one more file and it reports the folder once.
func TestTrashFolderAtAndAboveTheThreshold(t *testing.T) {
	for _, c := range []struct{ files int }{{WebhookFolderThreshold}, {WebhookFolderThreshold + 1}} {
		s := openTest(t)
		fx := newWebhookFixtureStore(t, s)
		newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.deleted")
		dir := bulkFolder(t, s, fx, c.files)
		trashDir(t, s, fx, dir)
		evs := eventsOf(t, s, fx.acc)
		if c.files == WebhookFolderThreshold {
			if len(evs) != c.files {
				t.Fatalf("%d files: %d events, want one per file", c.files, len(evs))
			}
			continue
		}
		checkSummary(t, evs, "file.deleted")
	}
}

// Moving a folder follows the same rule, as trashing it does.
func TestMoveFolderAtAndAboveTheThreshold(t *testing.T) {
	for _, files := range []int{WebhookFolderThreshold, WebhookFolderThreshold + 1} {
		s := openTest(t)
		fx := newWebhookFixtureStore(t, s)
		other := newFolderRow(t, s, fx.acc, "Other", "other")
		var otherRoot *Node
		if err := s.Write(context.Background(), func(tx *Tx) error {
			var err error
			otherRoot, err = tx.RootNode(context.Background(), fx.acc, other.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.moved")
		dir := bulkFolder(t, s, fx, files)
		if err := s.Write(context.Background(), func(tx *Tx) error {
			dir.FolderID, dir.ParentID = other.ID, otherRoot.ID
			if err := tx.UpdateNode(context.Background(), dir); err != nil {
				return err
			}
			if err := tx.MoveSubtree(context.Background(), fx.acc, dir.ID, fx.folder.ID, other.ID); err != nil {
				return err
			}
			return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
				ActorID: "key_test", Action: "node.move", Target: dir.ID, Folders: []string{fx.folder.ID, other.ID}})
		}); err != nil {
			t.Fatal(err)
		}
		evs := eventsOf(t, s, fx.acc)
		if files == WebhookFolderThreshold {
			if len(evs) != files {
				t.Fatalf("%d files: %d events, want one per file", files, len(evs))
			}
			continue
		}
		checkSummary(t, evs, "file.moved")
	}
}

// Restoring a folder follows the same rule: each file reports file.restored, or the folder reports it once.
func TestRestoreFolderAtAndAboveTheThreshold(t *testing.T) {
	for _, files := range []int{WebhookFolderThreshold, WebhookFolderThreshold + 1} {
		s := openTest(t)
		fx := newWebhookFixtureStore(t, s)
		newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.restored")
		dir := bulkFolder(t, s, fx, files)
		trashDir(t, s, fx, dir)
		if err := s.Write(context.Background(), func(tx *Tx) error {
			if err := tx.Untrash(context.Background(), fx.acc, dir.ID); err != nil {
				return err
			}
			return tx.InsertAudit(context.Background(), &AuditEntry{Time: Now(), AccountID: fx.acc, ActorKind: "account_key",
				ActorID: "key_test", Action: "node.restore", Target: dir.ID, Folders: []string{fx.folder.ID}})
		}); err != nil {
			t.Fatal(err)
		}
		evs := eventsOf(t, s, fx.acc)
		if files == WebhookFolderThreshold {
			if len(evs) != files {
				t.Fatalf("%d files: %d events, want one per file", files, len(evs))
			}
			continue
		}
		checkSummary(t, evs, "file.restored")
	}
}

// A folder with no files reports nothing when it is trashed or moved.
func TestEmptyFolderReportsNothing(t *testing.T) {
	s := openTest(t)
	fx := newWebhookFixtureStore(t, s)
	newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, webhookAllEvents()...)
	dir := bulkFolder(t, s, fx, 0)
	trashDir(t, s, fx, dir)
	if got := len(eventsOf(t, s, fx.acc)); got != 0 {
		t.Fatalf("trashing an empty folder wrote %d events, want 0", got)
	}
}
