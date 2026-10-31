// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// benchFolderTrash makes a folder with files files under root, in one untimed transaction, and returns it.
func benchFolderTrash(b *testing.B, s *Store, accountID string, f *Folder, root *Node, files int) *Node {
	b.Helper()
	ctx := context.Background()
	dir := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: f.ID, ParentID: root.ID, Name: "bulk",
		NameKey: "bulk", Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_bench", ModifiedBy: "usr_bench"}
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNode(ctx, dir); err != nil {
			return err
		}
		for i := 0; i < files; i++ {
			name := fmt.Sprintf("bulk-file-%04d.txt", i)
			n := &Node{ID: id.New("nod"), AccountID: accountID, FolderID: f.ID, ParentID: dir.ID, Name: name,
				NameKey: name, Kind: KindFile, Size: 1, BlobID: "blb_bench", ETag: `"e"`, CreatedAt: Now(), ModifiedAt: Now(),
				CreatedBy: "usr_bench", ModifiedBy: "usr_bench"}
			if err := tx.InsertNode(ctx, n); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	return dir
}

// benchFolderTrashWriter times the one transaction that trashes a folder of files files and audits it, the writer
// hold of that change. The folder is made outside the timer, each iteration.
func benchFolderTrashWriter(b *testing.B, endpoints, files int) {
	s, acc, f, root := benchOutbox(b, 0)
	ctx := context.Background()
	// benchOutbox subscribes its endpoints to file.created only, so these are made here for file.deleted.
	now := Now()
	for i := 0; i < endpoints; i++ {
		ep := &WebhookEndpoint{ID: id.New("whk"), AccountID: acc, URL: fmt.Sprintf("https://hook%d.example.com/in", i),
			Events: []string{"file.deleted"}, State: WebhookActive, SecretEnc: []byte("sealed"), CreatedBy: "key_bench",
			CreatedAt: now, UpdatedAt: now}
		if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertWebhookEndpoint(ctx, ep) }); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := benchFolderTrash(b, s, acc, f, root, files)
		b.StartTimer()
		if err := s.Write(ctx, func(tx *Tx) error {
			if err := tx.TrashNode(ctx, acc, dir.ID, "usr_bench", Now()); err != nil {
				return err
			}
			return tx.InsertAudit(ctx, &AuditEntry{Time: Now(), AccountID: acc, ActorKind: "account_key",
				ActorID: "key_bench", Action: "node.trash", Target: dir.ID, Folders: []string{f.ID}})
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTrashFolder1000NoEndpoints is the writer hold of trashing a 1,000-file folder in an account with no
// endpoint. It is the baseline for the next benchmark.
func BenchmarkTrashFolder1000NoEndpoints(b *testing.B) {
	benchFolderTrashWriter(b, 0, 1000)
}

// BenchmarkTrashFolder1000FiveEndpoints is the same trash with five active subscribers to file.deleted. At 1,000
// files the change is at the threshold, so it reports each file: 1,000 event rows and 5,000 deliveries.
func BenchmarkTrashFolder1000FiveEndpoints(b *testing.B) {
	benchFolderTrashWriter(b, 5, 1000)
}
