// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// BenchmarkEmitOneEndpoint is one EmitEvent call in an account with one subscriber: the per-file cost a copy pays with
// one endpoint, inside one transaction.
func BenchmarkEmitOneEndpoint(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 1)
	ctx := context.Background()
	n := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: root.ID, Name: "e.txt", NameKey: "e.txt",
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
