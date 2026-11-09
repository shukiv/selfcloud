// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// subNode is one node of the seeded trees. acc and folder default to account acc_s and folder fld_s.
type subNode struct {
	id, parent, kind string
	acc, folder      string
	covered, trashed bool
}

// seedSubtrees makes account acc_s with folder fld_s and root nod_root, account acc_o with folder fld_o and root
// nod_oroot, then the nodes given. A covered node carries no_download; a trashed one carries a trash mark.
func seedSubtrees(t *testing.T, s *Store, nodes []subNode) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_s', 'S', 'active', 0, 0)`,
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_o', 'O', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_s', 'acc_s', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_o', 'acc_o', 'Other', 'other', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_s', 'fld_s', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_oroot', 'acc_o', 'fld_o', NULL, '', '', 'dir', 0, 0)`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range nodes {
		acc, folder := n.acc, n.folder
		if acc == "" {
			acc, folder = "acc_s", "fld_s"
		}
		var trashed any
		if n.trashed {
			trashed = int64(1)
		}
		_, err := s.w.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size,
				no_download, trashed_at, created_at, modified_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 0, 0)`,
			n.id, acc, folder, n.parent, n.id, n.id, n.kind, boolToInt(n.covered), trashed)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestSubtreeHasNoDownloadCases: SubtreeHasNoDownload is true when the node or a node under it carries no_download, in its
// own account, and false otherwise. A trashed covered node counts, as it does for a create: the create route calls this same
// function, with no trash filter. Covered roots count too.
func TestSubtreeHasNoDownloadCases(t *testing.T) {
	s := openTest(t)
	seedSubtrees(t, s, []subNode{
		// An uncovered tree.
		{id: "nod_a", parent: "nod_root", kind: "dir"},
		{id: "nod_a1", parent: "nod_a", kind: "file"},
		// A covered direct child.
		{id: "nod_b", parent: "nod_root", kind: "dir"},
		{id: "nod_b1", parent: "nod_b", kind: "file", covered: true},
		// A covered node three levels down: nod_c, nod_c1, nod_c2, then nod_c3.
		{id: "nod_c", parent: "nod_root", kind: "dir"},
		{id: "nod_c1", parent: "nod_c", kind: "dir"},
		{id: "nod_c2", parent: "nod_c1", kind: "dir"},
		{id: "nod_c3", parent: "nod_c2", kind: "file", covered: true},
		// A trashed covered child.
		{id: "nod_d", parent: "nod_root", kind: "dir"},
		{id: "nod_d1", parent: "nod_d", kind: "file", covered: true, trashed: true},
		// A covered node in a sibling tree, outside nod_e.
		{id: "nod_e", parent: "nod_root", kind: "dir"},
		{id: "nod_f", parent: "nod_root", kind: "dir"},
		{id: "nod_f1", parent: "nod_f", kind: "file", covered: true},
		// A covered root.
		{id: "nod_g", parent: "nod_root", kind: "dir", covered: true},
		// A covered file in another account.
		{id: "nod_o1", parent: "nod_oroot", acc: "acc_o", folder: "fld_o", kind: "file", covered: true},
	})
	ctx := context.Background()
	for _, c := range []struct {
		name, acc, node string
		want            bool
	}{
		{"an uncovered tree", "acc_s", "nod_a", false},
		{"a covered direct child", "acc_s", "nod_b", true},
		{"a covered node three levels down", "acc_s", "nod_c", true},
		{"a trashed covered child, as a create sees it", "acc_s", "nod_d", true},
		{"the trashed covered child itself", "acc_s", "nod_d1", true},
		{"a covered node outside the subtree", "acc_s", "nod_e", false},
		{"a covered root", "acc_s", "nod_g", true},
		{"a covered node in another account, asked by this account", "acc_s", "nod_o1", false},
		{"the same node asked by its own account", "acc_o", "nod_o1", true},
	} {
		got, err := s.SubtreeHasNoDownload(ctx, c.acc, c.node)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: SubtreeHasNoDownload(%s, %s) = %v, want %v", c.name, c.acc, c.node, got, c.want)
		}
	}
}
