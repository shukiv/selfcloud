// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// BenchmarkCopyLabelValues measures the writer hold of one copy statement over a tree of labelled nodes: each node
// carries a text and a choice value, and the copy makes a node for each one. The targets are made outside the timer.
func BenchmarkCopyLabelValues(b *testing.B) {
	const nodes = 10000
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(b.TempDir(), "copy.db")})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_a', 'fld_a', NULL, '', '', 'dir', 0, 0)`,
		`INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
			VALUES ('lbf_text', 'acc_a', 'Note', 'note', 'text', 0, 0), ('lbf_status', 'acc_a', 'Status', 'status', 'choice', 0, 0)`,
		`INSERT INTO label_choices (id, field_id, name, name_key) VALUES ('lbc_open', 'lbf_status', 'Open', 'open')`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
	// The source tree: nodes with two values each, made in one transaction.
	pairs := make([]LabelCopy, 0, nodes)
	if err := s.Write(ctx, func(tx *Tx) error {
		for i := 0; i < nodes; i++ {
			from := fmt.Sprintf("nod_src_%05d", i)
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
				kind, created_at, modified_at) VALUES (?, 'acc_a', 'fld_a', 'nod_root', ?, ?, 'file', 0, 0)`, from, from, from); err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_text, set_at)
				VALUES (?, 'lbf_text', 'acc_a', 'a remark', 0)`, from); err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO node_labels (node_id, field_id, account_id, value_choice, set_at)
				VALUES (?, 'lbf_status', 'acc_a', 'lbc_open', 0)`, from); err != nil {
				return err
			}
			pairs = append(pairs, LabelCopy{From: from})
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		round := make([]LabelCopy, len(pairs))
		if err := s.Write(ctx, func(tx *Tx) error {
			for j, p := range pairs {
				to := fmt.Sprintf("nod_copy_%d_%05d", i, j)
				round[j] = LabelCopy{From: p.From, To: to}
				if _, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key,
					kind, created_at, modified_at) VALUES (?, 'acc_a', 'fld_a', 'nod_root', ?, ?, 'file', 0, 0)`, to, to, to); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := s.Write(ctx, func(tx *Tx) error { return tx.CopyLabelValues(ctx, "acc_a", round, true) }); err != nil {
			b.Fatal(err)
		}
	}
}
