// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/shukiv/selfcloud/internal/id"
)

// BenchmarkDecision300kNodes is the time of one report decision inside a write transaction on a tree of 300,000 nodes:
// the bounded count (countSubtree at WebhookNodeCap+1) and ReportFor. The tree is built untimed, with two statements
// (300 folders, then 299,700 files in them), so the setup is not the cost measured.
func BenchmarkDecision300kNodes(b *testing.B) {
	s, acc, f, root := benchOutbox(b, 1)
	ctx := context.Background()
	big := &Node{ID: id.New("nod"), AccountID: acc, FolderID: f.ID, ParentID: root.ID, Name: "big", NameKey: "big",
		Kind: KindDir, CreatedAt: Now(), ModifiedAt: Now(), CreatedBy: "usr_bench", ModifiedBy: "usr_bench"}
	now := millis(Now())
	if err := s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNode(ctx, big); err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `WITH RECURSIVE dirs(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM dirs WHERE i < 299)
			INSERT INTO nodes (`+nodeCols+`)
			SELECT 'nod_d' || i, ?, ?, ?, 'd' || i, 'd' || i, 'dir', 0, '', NULL, '', '', ?, ?, 'usr_bench', 'usr_bench',
				'usr_bench', 0, 0, NULL, NULL, NULL
			FROM dirs`, acc, f.ID, big.ID, now, now); err != nil {
			return err
		}
		_, err := tx.q.ExecContext(ctx, `WITH RECURSIVE dirs(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM dirs WHERE i < 299),
			files(j) AS (SELECT 0 UNION ALL SELECT j + 1 FROM files WHERE j < 998)
			INSERT INTO nodes (`+nodeCols+`)
			SELECT 'nod_f' || dirs.i || '_' || files.j, ?, ?, 'nod_d' || dirs.i, 'f' || files.j || '.txt', 'f' || files.j || '.txt',
				'file', 1, '', 'blb_bench', '"e"', 'ct' || dirs.i || '_' || files.j, ?, ?, 'usr_bench', 'usr_bench',
				'usr_bench', 0, 0, NULL, NULL, NULL
			FROM dirs CROSS JOIN files`, acc, f.ID, now, now)
		return err
	}); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	if err := s.Write(ctx, func(tx *Tx) error {
		for i := 0; i < b.N; i++ {
			nodes, files, err := tx.countSubtree(ctx, acc, big.ID, WebhookNodeCap+1)
			if err != nil {
				return err
			}
			if _, err := tx.ReportFor(ctx, acc, "file.created", nodes, files); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkTrashFolder1000HundredEndpoints is the writer hold of trashing a 1,000-file folder with 100 subscribers. The
// rule reports the folder once here (1,000 x 100 > WebhookDeliveryLimit), so this is the cost that was 12 s before.
func BenchmarkTrashFolder1000HundredEndpoints(b *testing.B) {
	benchFolderTrashWriter(b, 100, 1000)
}
