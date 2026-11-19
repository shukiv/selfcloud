// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
)

// changesSpace makes a team folder in the account and returns its ID and its root's ID.
func changesSpace(t *testing.T, st *store.Store, acc string) (string, string) {
	t.Helper()
	ctx := context.Background()
	fld := id.New("fld")
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertFolder(ctx, &store.Folder{ID: fld, AccountID: acc, Name: "Space", NameKey: "space",
			CreatedAt: store.Now(), UpdatedAt: store.Now()}, "test")
	}); err != nil {
		t.Fatal(err)
	}
	root, err := st.RootNode(ctx, acc, fld)
	if err != nil {
		t.Fatal(err)
	}
	return fld, root.ID
}

// insertChangesNode adds a file under the root, leaving a journal row timed by the trigger's clock.
func insertChangesNode(t *testing.T, st *store.Store, acc, folder, parent, nodeID string) {
	t.Helper()
	if err := st.Write(context.Background(), func(tx *store.Tx) error {
		return tx.InsertNode(context.Background(), &store.Node{ID: nodeID, AccountID: acc, FolderID: folder,
			ParentID: parent, Name: nodeID, NameKey: nodeID, Kind: store.KindFile, Size: 1, ETag: "e",
			CreatedAt: store.Now(), ModifiedAt: store.Now()})
	}); err != nil {
		t.Fatal(err)
	}
}

// ageChange sets one node's journal row time, through a second connection, as the trigger's clock cannot be told a time.
func ageChange(t *testing.T, path, nodeID string, at time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE node_changes SET at = ? WHERE node_id = ?`, at.UnixMilli(), nodeID); err != nil {
		t.Fatal(err)
	}
}

// countChanges counts a node's journal rows through a second connection.
func countChanges(t *testing.T, path, nodeID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM node_changes WHERE node_id = ?`, nodeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestChangesRetentionRemovesOnlyPastTheLimit: with a 30-day limit a row 40 days old goes, one exactly at the limit
// stays, and a young row stays; the count is logged and the mark rises.
func TestChangesRetentionRemovesOnlyPastTheLimit(t *testing.T) {
	ctx := context.Background()
	st, blobs, acc, path := openAuditStore(t)
	now := time.Now()
	day := 24 * time.Hour
	fld, root := changesSpace(t, st, acc)
	insertChangesNode(t, st, acc, fld, root, "nod_old")
	insertChangesNode(t, st, acc, fld, root, "nod_young")
	insertChangesNode(t, st, acc, fld, root, "nod_at")
	ageChange(t, path, "nod_old", now.Add(-40*day))
	ageChange(t, path, "nod_at", now.Add(-30*day))

	var logs bytes.Buffer
	sw := New(Options{Store: st, Blobs: blobs, ChangesRetention: 30 * day,
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChangesPruned != 1 {
		t.Errorf("pruned %d rows, want 1", res.ChangesPruned)
	}
	for nodeID, want := range map[string]int{"nod_old": 0, "nod_young": 1, "nod_at": 1} {
		if got := countChanges(t, path, nodeID); got != want {
			t.Errorf("%s: %d rows left, want %d", nodeID, got, want)
		}
	}
	mark, err := st.NodeChangesMark(ctx)
	if err != nil || mark <= 0 {
		t.Errorf("mark = %d, %v; want it raised above 0", mark, err)
	}
	if !strings.Contains(logs.String(), "changes journal pruned") || !strings.Contains(logs.String(), "removed=1") {
		t.Errorf("the count was not logged at info; log:\n%s", logs.String())
	}
}

// TestChangesRetentionZeroKeepsEverything: a zero retention keeps every row, however old.
func TestChangesRetentionZeroKeepsEverything(t *testing.T) {
	ctx := context.Background()
	st, blobs, acc, path := openAuditStore(t)
	now := time.Now()
	fld, root := changesSpace(t, st, acc)
	insertChangesNode(t, st, acc, fld, root, "nod_ancient")
	ageChange(t, path, "nod_ancient", now.Add(-1000*24*time.Hour))
	sw := New(Options{Store: st, Blobs: blobs, ChangesRetention: 0})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChangesPruned != 0 {
		t.Errorf("pruned %d rows with zero retention, want 0", res.ChangesPruned)
	}
	if got := countChanges(t, path, "nod_ancient"); got != 1 {
		t.Errorf("the ancient row was removed: %d left", got)
	}
}

// TestChangesRetentionPrunesDeletedAccount: a deleted account's rows go even while young, since no account holds them.
func TestChangesRetentionPrunesDeletedAccount(t *testing.T) {
	ctx := context.Background()
	st, blobs, acc, path := openAuditStore(t)
	now := time.Now()
	// A second account with a node, then the account is deleted; its journal row stays until the prune takes it.
	other := id.New("acc")
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.InsertAccount(ctx, &store.Account{ID: other, Name: "Gone", Status: store.AccountActive,
			CreatedAt: store.Now(), UpdatedAt: store.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	fld, root := changesSpace(t, st, other)
	insertChangesNode(t, st, other, fld, root, "nod_gone")
	if err := st.Write(ctx, func(tx *store.Tx) error {
		return tx.DeleteAccount(ctx, other)
	}); err != nil {
		t.Fatal(err)
	}
	// The node leaves two rows: its insert and the delete the account's cascade fires. Both stay until the prune.
	if got := countChanges(t, path, "nod_gone"); got != 2 {
		t.Fatalf("the deleted account's rows: %d, want 2 before the prune", got)
	}
	sw := New(Options{Store: st, Blobs: blobs, ChangesRetention: 30 * 24 * time.Hour})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChangesPruned < 1 {
		t.Errorf("pruned %d rows, want the deleted account's row gone", res.ChangesPruned)
	}
	if got := countChanges(t, path, "nod_gone"); got != 0 {
		t.Errorf("the deleted account's row survived: %d left", got)
	}
	_ = acc
}
