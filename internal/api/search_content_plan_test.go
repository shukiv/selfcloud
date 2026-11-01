// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestContentQueryPlanSortsNoMatchSet: the content candidate read orders by (content row, node). The match yields content
// rows in rowid order and each row's nodes come from the blob index in rowid order, so the planner needs no sort of the
// match set. Mutation: an ORDER BY that the indexes do not give in order makes SQLite sort the whole match set.
func TestContentQueryPlanSortsNoMatchSet(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "a.txt", "planword")
	drainText(t, fx.s.textWorker)
	req := &search.Request{AccountID: fx.acc.ID, In: search.InContent, Terms: []string{"planword"}, ModifiedFrom: -1,
		ModifiedTo: -1, SizeMin: -1, SizeMax: -1, Limit: 25}
	query, args, err := req.ContentSQL(search.Pos{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	err = fx.s.store.Read(context.Background(), func(q store.Querier) error {
		rows, err := q.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				return err
			}
			plan = append(plan, detail)
		}
		t.Logf("content plan: %s", strings.Join(plan, " | "))
		for _, d := range plan {
			if strings.Contains(d, "TEMP B-TREE") {
				t.Errorf("the content query sorts: %s", d)
			}
		}
		if len(plan) == 0 || !strings.HasPrefix(plan[0], "SCAN search_text_fts") {
			t.Errorf("the full-text table is not the outer loop: %v", plan)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestStagedCopyContentIsNeverFound: a staged node, the copy in progress, is never a content hit, even when its blob has
// matching text. Mutation: the staged filter removed from the content query makes the copy show up.
func TestStagedCopyContentIsNeverFound(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	fx.file(t, "source.txt", "stagedword")
	drainText(t, fx.s.textWorker)
	var blob string
	err := fx.s.store.Read(context.Background(), func(q store.Querier) error {
		return q.QueryRowContext(context.Background(), "SELECT blob_id FROM nodes WHERE name = 'source.txt'").Scan(&blob)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = fx.s.store.Write(context.Background(), func(tx *store.Tx) error {
		now := time.Now()
		if err := tx.CreateStagingFolder(context.Background(), fx.acc.ID, "fld_staged1", "nod_staged1", now); err != nil {
			return err
		}
		return tx.InsertNodeNoIndex(context.Background(), &store.Node{ID: "nod_staged2", AccountID: fx.acc.ID,
			FolderID: "fld_staged1", ParentID: "nod_staged1", Name: "copy.txt", NameKey: "copy.txt", Kind: store.KindFile,
			BlobID: blob, Size: 10, CreatedAt: now, ModifiedAt: now, CreatedBy: "usr_none", ModifiedBy: "usr_none"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := contentNames(fx.find(t, fx.bobSess, "stagedword")); fmt.Sprint(got) != "[source.txt]" {
		t.Errorf("a staged copy showed up in content search: %v", got)
	}
}
