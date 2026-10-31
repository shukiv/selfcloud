// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// benchOutbox is an account with one team folder and its root, and endpoints active endpoints that subscribe to
// file.created. It takes testing.TB, so the benchmarks can use it.
func benchOutbox(b *testing.B, endpoints int) (*Store, string, *Folder, *Node) {
	b.Helper()
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(b.TempDir(), "bench.db")})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	now := Now()
	acc := &Account{ID: id.New("acc"), Name: "Bench", Status: AccountActive, CreatedAt: now, UpdatedAt: now}
	f := &Folder{ID: id.New("fld"), AccountID: acc.ID, Name: "Team", NameKey: "team", CreatedAt: now, UpdatedAt: now}
	var root *Node
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertAccount(ctx, acc); err != nil {
			return err
		}
		if err := tx.InsertFolder(ctx, f, "usr_bench"); err != nil {
			return err
		}
		var err error
		root, err = tx.RootNode(ctx, acc.ID, f.ID)
		return err
	}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < endpoints; i++ {
		ep := &WebhookEndpoint{ID: id.New("whk"), AccountID: acc.ID, URL: fmt.Sprintf("https://hook%d.example.com/in", i),
			Events: []string{"file.created"}, State: WebhookActive, SecretEnc: []byte("sealed"), CreatedBy: "key_bench",
			CreatedAt: now, UpdatedAt: now}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertWebhookEndpoint(ctx, ep) }); err != nil {
			b.Fatal(err)
		}
	}
	return s, acc.ID, f, root
}

// benchCreate writes one file and audits it under action in one transaction, as a handler does.
func benchCreate(b *testing.B, s *Store, accountID string, f *Folder, root *Node, action string) {
	b.Helper()
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		n := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: f.ID, ParentID: root.ID, Name: name,
			NameKey: name, Kind: KindFile, Size: 5, BlobID: "blb_bench", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now(),
			CreatedBy: "usr_bench", ModifiedBy: "usr_bench"}
		if err := s.Write(ctx, func(tx *Tx) error {
			if err := tx.InsertNode(ctx, n); err != nil {
				return err
			}
			return tx.InsertAudit(ctx, &AuditEntry{Time: Now(), AccountID: accountID, ActorKind: "account_key",
				ActorID: "key_bench", Action: action, Target: n.ID, Folders: []string{f.ID}})
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChangeWithoutOutbox is the baseline: a file and its audit entry, under an action that maps to no event,
// so the outbox returns before it reads anything.
func BenchmarkChangeWithoutOutbox(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 0)
	benchCreate(b, s, acc, f, root, "folder.settings")
}

// BenchmarkChangeNoEndpoints is the same change under an action that maps to file.created, in an account with no
// endpoint. The difference from the baseline is what the outbox costs such an account.
func BenchmarkChangeNoEndpoints(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 0)
	benchCreate(b, s, acc, f, root, "node.create")
}

// BenchmarkChangeOneEndpoint is the change with one active subscriber: one event row and one delivery.
func BenchmarkChangeOneEndpoint(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 1)
	benchCreate(b, s, acc, f, root, "node.create")
}

// BenchmarkChangeFiveEndpoints is the change with five active subscribers.
func BenchmarkChangeFiveEndpoints(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 5)
	benchCreate(b, s, acc, f, root, "node.create")
}

// BenchmarkEmitNoEndpoints is one EmitEvent call in an account with no endpoint: the per-file cost a copy of a large
// tree pays with no subscriber, inside one transaction.
func BenchmarkEmitNoEndpoints(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 0)
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
