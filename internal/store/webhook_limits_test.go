// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// bulkTree inserts a folder named bulk under the fixture's root, with dirs folders and files files directly in it,
// in one transaction. The node count of the tree is 1 + dirs + files, the root included.
func bulkTree(t *testing.T, s *Store, fx webhookFixtureStore, dirs, files int) *Node {
	t.Helper()
	ctx := context.Background()
	dir := &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.folder.ID, ParentID: fx.root.ID, Name: "bulk",
		NameKey: "bulk", Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_test", ModifiedBy: "usr_test"}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNode(ctx, dir); err != nil {
			return err
		}
		for i := 0; i < dirs; i++ {
			name := fmt.Sprintf("sub-%05d", i)
			sub := &Node{ID: id.New("nod"), AccountID: fx.acc, FolderID: fx.folder.ID, ParentID: dir.ID, Name: name,
				NameKey: name, Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_test", ModifiedBy: "usr_test"}
			if err := tx.InsertNode(ctx, sub); err != nil {
				return err
			}
		}
		for i := 0; i < files; i++ {
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

// The change reports each file when it touches at most WebhookFolderThreshold files and the files times the endpoints
// that subscribe to the type stay within WebhookDeliveryLimit. Past either, it reports the folder once. The cases sit
// on both sides of each limit (decision 27).
func TestPerFileBoundaries(t *testing.T) {
	for _, c := range []struct {
		files, endpoints int
		perFile          bool
	}{
		{1000, 5, true},
		{1001, 1, false},
		{1000, 6, false},
		{100, 50, true},
		{101, 50, false},
	} {
		t.Run(fmt.Sprintf("%dx%d", c.files, c.endpoints), func(t *testing.T) {
			s := openTest(t)
			fx := newWebhookFixtureStore(t, s)
			for i := 0; i < c.endpoints; i++ {
				newEndpointState(t, s, fx.acc, fmt.Sprintf("https://h%d.example.com/in", i), WebhookActive, "file.deleted")
			}
			dir := bulkFolder(t, s, fx, c.files)
			trashDir(t, s, fx, dir)
			evs := eventsOf(t, s, fx.acc)
			if c.perFile {
				if len(evs) != c.files {
					t.Fatalf("%d files, %d endpoints: %d events, want one per file", c.files, c.endpoints, len(evs))
				}
				return
			}
			checkSummary(t, evs, "file.deleted")
		})
	}
}

// A tree of more than WebhookNodeCap nodes is reported as a folder whatever its file count: the walk that counts it
// stops at the cap, so the tree is never loaded. Ten thousand nodes, the root included, still report each file.
func TestNodeCapBoundary(t *testing.T) {
	for _, c := range []struct {
		dirs, files int
		perFile     bool
	}{
		{9998, 1, true},   // 1 + 9998 + 1 = 10000 nodes
		{10000, 1, false}, // 1 + 10000 + 1 = 10002 nodes: the bulk folder holds 10,001 folders and one file
	} {
		s := openTest(t)
		fx := newWebhookFixtureStore(t, s)
		newEndpointState(t, s, fx.acc, "https://a.example.com/in", WebhookActive, "file.deleted")
		dir := bulkTree(t, s, fx, c.dirs, c.files)
		trashDir(t, s, fx, dir)
		evs := eventsOf(t, s, fx.acc)
		if c.perFile {
			if len(evs) != c.files {
				t.Fatalf("%d nodes: %d events, want one per file", c.dirs+c.files+1, len(evs))
			}
			continue
		}
		checkSummary(t, evs, "file.deleted")
	}
}
