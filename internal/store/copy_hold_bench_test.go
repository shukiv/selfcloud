// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkCopyHold measures the writer hold of one whole folder copy in one write, the way a copy of
// copy.max_nodes nodes runs: the subtree is read, every node is inserted, and (with labels) every label
// value is copied. The hold is the time inside the write, from the start of the callback to the commit,
// reported as the hold-ms/op metric (the mean over b.N runs).
//
// Set TMPDIR to choose the disk for the database, since b.TempDir follows it.
func BenchmarkCopyHold(b *testing.B) {
	for _, tc := range []struct {
		name   string
		labels bool
	}{{"no_labels", false}, {"label_every_node", true}} {
		b.Run(tc.name, func(b *testing.B) {
			benchCopyHold(b, copyHoldNodes, tc.labels)
		})
	}
}

// copyHoldNodes is the size the default copy.max_nodes allows.
const copyHoldNodes = 20000

func benchCopyHold(b *testing.B, nodes int, labels bool) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(b.TempDir(), "copy-hold.db")})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	setup := []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
			VALUES ('lbf_text', 'acc_a', 'Note', 'note', 'text', 0, 0)`,
	}
	for _, q := range setup {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
	// The source: a folder holding the tree, with its nodes and, when labels is set, a value on each.
	if err := s.Write(ctx, func(tx *Tx) error {
		if _, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
			kind, created_at, modified_at) VALUES ('nod_src', 'acc_a', 'fld_a', 'nod_root', 'src', 'src', 'dir', 0, 0)`); err != nil {
			return err
		}
		for i := 1; i < nodes; i++ {
			id := fmt.Sprintf("nod_src_%06d", i)
			name := fmt.Sprintf("file-%06d.txt", i)
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
				kind, size, etag, created_at, modified_at) VALUES (?, 'acc_a', 'fld_a', 'nod_src', ?, ?, 'file', 1, 'e', 0, 0)`,
				id, name, name); err != nil {
				return err
			}
			if labels {
				if _, err := tx.q.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_text, set_at)
					VALUES (?, 'lbf_text', 'acc_a', 'a remark', 0)`, id); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	var total time.Duration
	for i := 0; i < b.N; i++ {
		root := fmt.Sprintf("nod_copy_%d", i)
		start := time.Now()
		err := s.Write(ctx, func(tx *Tx) error {
			tree, err := tx.Subtree(ctx, "acc_a", "nod_src")
			if err != nil {
				return err
			}
			newIDs := make(map[string]string, len(tree))
			pairs := make([]LabelCopy, 0, len(tree))
			for j, n := range tree {
				c := *n
				if j == 0 {
					c.ID = root
				} else {
					c.ID = fmt.Sprintf("%s_%06d", root, j)
				}
				newIDs[n.ID] = c.ID
				c.FolderID = "fld_a"
				c.CreatedAt, c.ModifiedAt = Now(), Now()
				c.OwnerID = ""
				if j == 0 {
					c.ParentID, c.Name, c.NameKey = "nod_root", "copy-"+root, "copy-"+root
				} else {
					c.ParentID = newIDs[n.ParentID]
				}
				if err := tx.InsertNode(ctx, &c); err != nil {
					return err
				}
				pairs = append(pairs, LabelCopy{From: n.ID, To: c.ID})
			}
			if labels {
				return tx.CopyLabelValues(ctx, "acc_a", pairs, true)
			}
			return nil
		})
		total += time.Since(start)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(total.Milliseconds())/float64(b.N), "hold-ms/op")
}
