// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// lockBenchTree builds one account with a folder. Its files are at depth 10 under a chain of directories, and lockCount
// of them are held by live locks. It returns the store, the account, a directory at the top that holds no lock (the
// folder a subtree check tests) and the file that a benchmark writes, which has no lock.
func lockBenchTree(b *testing.B, lockCount int) (*Store, string, *Node, *Node) {
	b.Helper()
	s := openTest(b)
	ctx := context.Background()
	acc := newAccountRow(b, s, "bench")
	f := &Folder{ID: id.New("fld"), AccountID: acc, Name: "Team", NameKey: "team", CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertFolder(ctx, f, "usr_bench") }); err != nil {
		b.Fatal(err)
	}
	parent, err := s.RootNode(ctx, acc, f.ID)
	if err != nil {
		b.Fatal(err)
	}
	dirs := []*Node{parent}
	for depth := range 10 {
		d := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: parent.ID, Name: fmt.Sprint("d", depth),
			NameKey: fmt.Sprint("d", depth), Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_bench",
			ModifiedBy: "usr_bench", ETag: "e"}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, d) }); err != nil {
			b.Fatal(err)
		}
		dirs = append(dirs, d)
		parent = d
	}
	deepest := dirs[len(dirs)-1]
	insertFile := func(name string, under *Node) *Node {
		n := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: under.ID, Name: name, NameKey: name,
			Kind: KindFile, Size: 1, ETag: "e", CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_bench",
			ModifiedBy: "usr_bench"}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, n) }); err != nil {
			b.Fatal(err)
		}
		return n
	}
	for i := range lockCount {
		locked := insertFile(fmt.Sprint("locked", i, ".docx"), deepest)
		now := Now()
		if err := s.Write(ctx, func(tx *Tx) error {
			_, err := tx.PutLock(ctx, &WOPILock{NodeID: locked.ID, AccountID: acc, LockID: fmt.Sprint("L", i),
				Holder: "usr_bench", CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute)})
			return err
		}); err != nil {
			b.Fatal(err)
		}
	}
	side := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: parent.ID, Name: "side", NameKey: "side",
		Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_bench", ModifiedBy: "usr_bench", ETag: "e"}
	if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertNode(ctx, side) }); err != nil {
		b.Fatal(err)
	}
	return s, acc, side, insertFile("target.docx", deepest)
}

// BenchmarkUpdateNodeGuard measures one node write, the store's UpdateNode inside its own write transaction, with the
// guard's check of the account's live locks. The file written has no lock. The first case has no live lock in the
// account, the cost the guard adds to every ordinary write; the others add locks, in the same account, to show how the
// cost grows with the number of editors that hold a file.
func BenchmarkUpdateNodeGuard(b *testing.B) {
	for _, locks := range []int{0, 8, 64, MaxLiveLocks} {
		b.Run(fmt.Sprintf("live_locks_%d", locks), func(b *testing.B) {
			s, acc, _, target := lockBenchTree(b, locks)
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Write(ctx, func(tx *Tx) error {
					n := *target
					return tx.UpdateNode(ctx, &n)
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			_ = acc
		})
	}
}

// BenchmarkSubtreeGuard measures the subtree check alone, which a folder operation runs: the check reads the account's
// live locks, and walks up from each locked file to the folder it is tested against. The folder is a top-level one that
// holds no lock, so every walk runs the full depth of the chain before it answers no.
func BenchmarkSubtreeGuard(b *testing.B) {
	for _, locks := range []int{0, 8, 64, MaxLiveLocks} {
		b.Run(fmt.Sprintf("live_locks_%d", locks), func(b *testing.B) {
			s, acc, side, _ := lockBenchTree(b, locks)
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Write(ctx, func(tx *Tx) error {
					return tx.guardNode(ctx, acc, side.ID, "")
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
