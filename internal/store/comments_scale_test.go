// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"
)

// TestCommentsAtScale measures the comment statements on a populated database: 1,000,000 comments in the table, 5,000 of
// them on one node, and 10,000 users in one account. It is a measurement, not a gate, so it runs only when
// SELFCLOUD_SCALE is set, once and by hand, at low priority, with TMPDIR on /var/tmp (decision 39 reports the numbers).
// The rows name no real account, node or user, so foreign keys are off for the fixture only; the plans are read on the
// real schema, and TestCommentsSQLDoesNotScan covers them.
func TestCommentsAtScale(t *testing.T) {
	if os.Getenv("SELFCLOUD_SCALE") == "" {
		t.Skip("set SELFCLOUD_SCALE=1 to measure at 1,000,000 comments and 10,000 users (run once, by hand)")
	}
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.w.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	const hot = "nod_hot"
	const acc = "acc_scale"

	start := time.Now()
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 999999)
		INSERT INTO comments (id, account_id, node_id, parent_id, author_id, body, created_at)
		SELECT printf('cmt_%026d', i), ?, CASE WHEN i < 5000 THEN ? ELSE printf('nod_%04d', i % 1000) END,
			NULL, NULL, 'a comment', i FROM n`, acc, hot); err != nil {
		t.Fatal(err)
	}
	t.Logf("inserted 1,000,000 comments (5,000 on %s) in %v", hot, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 9999)
		INSERT INTO users (id, account_id, email, email_key, display_name, role, status, created_at, updated_at)
		SELECT printf('usr_%026d', i), ?, 'u' || i || '@example.test', 'u' || i || '@example.test',
			CASE WHEN i % 10 = 0 THEN 'Pat ' || i ELSE 'Person ' || i END, 'member', 'active', i, i FROM n`, acc); err != nil {
		t.Fatal(err)
	}
	t.Logf("inserted 10,000 users in %v", time.Since(start).Round(time.Millisecond))

	measure := func(label string, n int, fn func() error) {
		t.Helper()
		var ds []time.Duration
		for i := 0; i < n; i++ {
			at := time.Now()
			if err := fn(); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			ds = append(ds, time.Since(at))
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		t.Logf("%-46s median %8v   worst %8v   (%d runs)", label, ds[len(ds)/2].Round(time.Microsecond),
			ds[len(ds)-1].Round(time.Microsecond), n)
	}

	measure("list: top-level page of 50, hot node", 30, func() error {
		_, err := s.TopLevelComments(ctx, acc, hot, Page{Limit: 50})
		return err
	})
	deep := ""
	if cs, err := s.TopLevelComments(ctx, acc, hot, Page{Limit: 2500}); err == nil && len(cs) == 2500 {
		deep = CommentCursor(cs[len(cs)-1])
	} else {
		t.Fatalf("no deep cursor: %v", err)
	}
	measure("list: top-level page of 50, after 2,500 rows", 30, func() error {
		_, err := s.TopLevelComments(ctx, acc, hot, Page{Limit: 50, Cursor: deep})
		return err
	})
	measure("count: live comments, capped at 5,001 (the cap check)", 30, func() error {
		return s.Write(ctx, func(tx *Tx) error {
			_, err := tx.CountLiveComments(ctx, acc, hot, maxScaleCap+1)
			return err
		})
	})

	// The worst write of a comment is one at the cap: the count reads the full 5,000 and the write is refused, so no insert
	// runs. The count is then the whole cost. A write to a node with room is the count plus one insert.
	var worst time.Duration
	var total time.Duration
	const writes = 30
	for i := 0; i < writes; i++ {
		at := time.Now()
		err := s.Write(ctx, func(tx *Tx) error {
			live, err := tx.CountLiveComments(ctx, acc, hot, maxScaleCap)
			if err != nil {
				return err
			}
			if live >= maxScaleCap {
				return errScaleCap
			}
			return nil
		})
		if err != nil && err != errScaleCap {
			t.Fatal(err)
		}
		d := time.Since(at)
		total += d
		if d > worst {
			worst = d
		}
	}
	t.Logf("%-46s mean %8v   worst %8v   (%d writes, refused at the cap)", "write: count at the cap, refused",
		(total / writes).Round(time.Microsecond), worst.Round(time.Microsecond), writes)

	// A write to a node with room: the count and one insert, on a node of 1,000 comments.
	const room = "nod_0007"
	total, worst = 0, 0
	for i := 0; i < writes; i++ {
		at := time.Now()
		err := s.Write(ctx, func(tx *Tx) error {
			if _, err := tx.CountLiveComments(ctx, acc, room, maxScaleCap); err != nil {
				return err
			}
			return tx.InsertComment(ctx, &Comment{ID: "cmt_room" + strconv.Itoa(i) + "_" + acc, AccountID: acc,
				NodeID: room, Body: "a write", CreatedAt: Now()})
		})
		if err != nil {
			t.Fatal(err)
		}
		d := time.Since(at)
		total += d
		if d > worst {
			worst = d
		}
	}
	t.Logf("%-46s mean %8v   worst %8v   (%d writes)", "write: count and insert, node with room",
		(total / writes).Round(time.Microsecond), worst.Round(time.Microsecond), writes)

	measure("people: 'Person' (90% of users match), 50 kept", 20, func() error {
		_, err := s.ActiveUsersNamed(ctx, acc, "Person", 50)
		return err
	})
	measure("people: 'Pat' (10% match), 50 kept", 20, func() error {
		_, err := s.ActiveUsersNamed(ctx, acc, "Pat", 50)
		return err
	})
	measure("people: 'zzz' (no match, every user read)", 20, func() error {
		_, err := s.ActiveUsersNamed(ctx, acc, "zzz", 50)
		return err
	})
}

// maxScaleCap is the live-comment cap of a node (the API's maxCommentsPerNode), held here because the store cannot import the API.
const maxScaleCap = 5000

// errScaleCap is the refusal a write at the cap gets in the measurement.
var errScaleCap = errors.New("at the cap")
