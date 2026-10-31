// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// BenchmarkEmitFiftyEndpoints is one EmitEvent with fifty subscribers: fifty deliveries and one event row, inside one
// transaction. The per-delivery cost is this divided by fifty, less the event row.
func BenchmarkEmitFiftyEndpoints(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 50)
	ctx := context.Background()
	n := &Node{ID: "nod_bench_emit", AccountID: acc, FolderID: f.ID, ParentID: root.ID, Name: "e.txt", NameKey: "e.txt",
		Kind: KindFile, Size: 5}
	if err := s.Write(ctx, func(tx *Tx) error {
		for i := 0; i < b.N; i++ {
			if err := tx.EmitEvent(ctx, acc, "file.created", Now(), FileEvent(n)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
}
