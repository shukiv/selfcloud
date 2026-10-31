// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/id"
)

// BenchmarkCopyPublish measures the writer hold of publishing a staged copy at the sizes the default copy.max_nodes
// allows. The database is built the way a real copy leaves it: the source tree is in it too (its nodes and labels,
// which the copy read), and the staged tree's rows carry derived IDs and were written in a shuffled order, as the
// copy's batches write them (the snapshot is read in ID order, and a batch's rows are chosen by the cursor). The
// timed write is the publish: it reads the new root and checks the name in the destination, moves every staged
// node's folder_id, sets the new root's parent and name, settles the reservation, writes the audit entry and one
// folder-summary event, and deletes the staging folder.
//
// The hold is the time inside the write, reported as hold-ms/op. Each sub-benchmark builds a fresh database, so run
// it with -test.benchtime 1x -test.count N. Set TMPDIR to choose the disk.
func BenchmarkCopyPublish(b *testing.B) {
	for _, n := range []int{20000, 50000, 100000} {
		b.Run(fmt.Sprintf("nodes_%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				publishHold(b, n)
			}
		})
	}
}

// publishHold builds the database for one copy of n nodes, publishes it once, and reports the hold.
func publishHold(b *testing.B, n int) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(b.TempDir(), "publish.db")})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	const (
		acc     = "acc_a"
		dest    = "fld_dest"
		destRt  = "nod_dest_root"
		staging = "fld_stage"
		stageRt = "nod_stage_root"
		src     = "fld_src"
		srcRt   = "nod_src_root"
		srcTree = "nod_src"
		root    = "nod_copy"
		dirs    = 100
	)
	perDir := (n - 1 - dirs) / dirs
	// tree writes one tree of n nodes (dirs directories, each with perDir files, each file with a label) under
	// folder fld, whose root is rt and whose copied top is top. Its IDs are derived from prefix and the position,
	// and its rows are written in a shuffled order.
	tree := func(tx *Tx, prefix, fld, parent, top, topName string) error {
		q := func(sql string, args ...any) error {
			_, err := tx.q.ExecContext(ctx, sql, args...)
			return err
		}
		if err := q(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES (?, ?, ?, ?, ?, ?, 'dir', 0, 0)`, top, acc, fld, parent, topName, topName); err != nil {
			return err
		}
		type item struct {
			id, parent, name string
			dir              bool
		}
		var items []item
		for d := 0; d < dirs; d++ {
			dir := id.Derive("nod", fmt.Sprintf("%s/dir/%d", prefix, d))
			items = append(items, item{id: dir, parent: top, name: dir, dir: true})
			for i := 0; i < perDir; i++ {
				items = append(items, item{id: id.Derive("nod", fmt.Sprintf("%s/file/%d/%d", prefix, d, i)),
					parent: dir, name: fmt.Sprintf("file-%05d-%03d.txt", i, d)})
			}
		}
		// The files are shuffled; the directories go first, since a file names its directory as its parent.
		rand.New(rand.NewSource(1)).Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
		sort.SliceStable(items, func(i, j int) bool { return items[i].dir && !items[j].dir })
		for _, it := range items {
			if it.dir {
				if err := q(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
					VALUES (?, ?, ?, ?, ?, ?, 'dir', 0, 0)`, it.id, acc, fld, it.parent, it.name, it.name); err != nil {
					return err
				}
				continue
			}
			if err := q(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, etag, created_at, modified_at)
				VALUES (?, ?, ?, ?, ?, ?, 'file', 1, 'e', 0, 0)`, it.id, acc, fld, it.parent, it.name, it.name); err != nil {
				return err
			}
			if err := q(`INSERT INTO node_labels (node_id, field_id, account_id, value_text, set_at)
				VALUES (?, 'lbf_text', ?, 'a remark', 0)`, it.id, acc); err != nil {
				return err
			}
		}
		return nil
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		q := func(sql string, args ...any) error {
			_, err := tx.q.ExecContext(ctx, sql, args...)
			return err
		}
		if err := q(`INSERT INTO accounts (id, name, status, created_at, updated_at, reserved_bytes) VALUES (?, 'A', 'active', 0, 0, ?)`,
			acc, int64(n)); err != nil {
			return err
		}
		for _, f := range []string{dest, src} {
			if err := q(`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at, reserved_bytes) VALUES (?, ?, ?, ?, 0, 0, 0)`,
				f, acc, f, f); err != nil {
				return err
			}
		}
		if err := q(`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at, reserved_bytes) VALUES (?, ?, 'Stage', 'stage', 0, 0, ?)`,
			staging, acc, int64(n)); err != nil {
			return err
		}
		for _, rt := range []string{destRt, stageRt, srcRt} {
			fld := map[string]string{destRt: dest, stageRt: staging, srcRt: src}[rt]
			if err := q(`INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
				VALUES (?, ?, ?, NULL, '', '', 'dir', 0, 0)`, rt, acc, fld); err != nil {
				return err
			}
		}
		if err := q(`INSERT INTO label_fields (id, account_id, name, name_key, type, created_at, updated_at)
			VALUES ('lbf_text', ?, 'Note', 'note', 'text', 0, 0)`, acc); err != nil {
			return err
		}
		if err := tree(tx, "src", src, srcRt, srcTree, srcTree); err != nil {
			return err
		}
		return tree(tx, "staged", staging, stageRt, root, "copy")
	}); err != nil {
		b.Fatal(err)
	}
	files := int64(dirs * perDir)
	start := time.Now()
	err = s.Write(ctx, func(tx *Tx) error {
		cur, err := tx.Node(ctx, acc, root)
		if err != nil {
			return err
		}
		if _, err := tx.Child(ctx, acc, destRt, cur.NameKey); err != nil && err != ErrNotFound {
			return err
		}
		// Every node of the staged tree moves to the destination: one statement, the staged folder's rows, as the
		// publish does it (PublishStaged).
		if _, err := tx.q.ExecContext(ctx, `UPDATE nodes SET folder_id = ?,
			owner_id = COALESCE((SELECT owner_user_id FROM folders WHERE id = ? AND kind = 'personal'), owner_id)
			WHERE account_id = ? AND folder_id = ? AND parent_id IS NOT NULL`,
			dest, dest, acc, staging); err != nil {
			return err
		}
		cur.ParentID, cur.FolderID, cur.ModifiedAt = destRt, dest, Now()
		if err := tx.UpdateNode(ctx, cur); err != nil {
			return err
		}
		if err := tx.AdjustFolders(ctx, acc,
			FolderChange{FolderID: staging, Change: UsageChange{Reserved: -int64(n)}},
			FolderChange{FolderID: dest, Change: UsageChange{Bytes: files, Files: files}}); err != nil {
			return err
		}
		if err := tx.InsertAudit(ctx, &AuditEntry{Time: Now(), AccountID: acc, ActorKind: "key", ActorID: "key_a",
			Action: "node.copy", Target: root, Details: map[string]any{"nodes": n}, Folders: []string{dest}}); err != nil {
			return err
		}
		if err := tx.EmitEvent(ctx, acc, "file.created", Now(), FolderEvent(cur)); err != nil {
			return err
		}
		_, err = tx.q.ExecContext(ctx, `DELETE FROM folders WHERE account_id = ? AND id = ?`, acc, staging)
		return err
	})
	hold := time.Since(start)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(hold.Milliseconds()), "hold-ms/op")
}
