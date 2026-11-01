// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

// TestMeasureActivityReads measures a read of a node's activity on a large audit table, and the cost the new index adds to
// an audit insert. The table holds SELFCLOUD_MEASURE_ROWS rows (1,000,000 by default) of two accounts spread over 10,000
// nodes, plus one hot node of the first account with 100,000 more rows of which one in a thousand is a permission
// change. It reports the median, the 95th percentile and the longest of 200 reads for a node with few rows, for the first
// and a deep page of the hot node, and for the hot node when only the rare kind is asked for. It then times 5,000 audit
// inserts, one write each, with the index and without it. It runs only when SELFCLOUD_MEASURE_ACTIVITY is set.
func TestMeasureActivityReads(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_ACTIVITY") == "" {
		t.Skip("set SELFCLOUD_MEASURE_ACTIVITY=1 to run the activity measurement")
	}
	rows := 1_000_000
	if v := os.Getenv("SELFCLOUD_MEASURE_ROWS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		rows = n
	}
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "measure.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Unix(1_700_000_000, 0)
	for _, id := range []string{"acc_a", "acc_b"} {
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.InsertAccount(ctx, &Account{ID: id, Name: id, Status: AccountActive, CreatedAt: at, UpdatedAt: at})
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed := time.Now()
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO audit_log (time, account_id, actor_kind, actor_id, action, target, ip, user_agent, outcome, details)
		SELECT 1700000000000 + i, CASE WHEN i % 2 = 0 THEN 'acc_a' ELSE 'acc_b' END, 'user', 'usr_' || (i % 50),
		       CASE WHEN i % 10 = 0 THEN 'node.rename' ELSE 'node.write' END, 'nod_' || (i % 10000),
		       '', 'measure', 'ok', '{"size":1234,"previous_size":1200}' FROM n`, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 100000)
		INSERT INTO audit_log (time, account_id, actor_kind, actor_id, action, target, ip, user_agent, outcome, details)
		SELECT 1700100000000 + i, 'acc_a', 'user', 'usr_' || (i % 50),
		       CASE WHEN i % 1000 = 0 THEN 'permission.set' ELSE 'node.write' END, 'nod_hot',
		       '', 'measure', 'ok', '{"size":1234,"previous_size":1200}' FROM n`); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d rows in %s", total, time.Since(seed).Round(time.Millisecond))

	timeIt := func(name string, q ActivityRead) {
		t.Helper()
		var d []time.Duration
		var got int
		for i := 0; i < 200; i++ {
			start := time.Now()
			pg, err := s.Activity(ctx, q, 0)
			r := pg.Rows
			if err != nil {
				t.Fatal(err)
			}
			d = append(d, time.Since(start))
			got = len(r)
		}
		slices.Sort(d)
		t.Logf("%-46s rows returned %3d  median %8s  p95 %8s  max %8s", name, got, d[len(d)/2], d[len(d)*95/100], d[len(d)-1])
	}
	base := ActivityRead{AccountID: "acc_a", ViewerUserID: "usr_1", Limit: 50, HideRestricted: true}
	content := []string{"node.write", "node.rename", "node.create", "node.move"}
	cold := base
	cold.NodeID, cold.Actions = "nod_42", content
	timeIt("node with 50 rows, first page", cold)
	hot := base
	hot.NodeID, hot.Actions = "nod_hot", content
	timeIt("hot node (100,000 rows), first page", hot)
	var mid int64
	if err := s.w.QueryRowContext(ctx, `SELECT id FROM audit_log WHERE target = 'nod_hot' ORDER BY id LIMIT 1 OFFSET 50000`).Scan(&mid); err != nil {
		t.Fatal(err)
	}
	deep := hot
	deep.Before = mid
	timeIt("hot node, page after row 50,000 from the end", deep)
	rare := base
	rare.NodeID, rare.Actions = "nod_hot", []string{"permission.set", "permission.expired"}
	timeIt("hot node, only the rare kind (100 of 100,000)", rare)
	oldest := rare
	oldest.Limit = 100
	timeIt("hot node, rare kind, limit 100 (reads to the end)", oldest)

	insert := func(label string) {
		start := time.Now()
		for i := 0; i < 5000; i++ {
			e := AuditEntry{Time: at, AccountID: "acc_a", ActorKind: "user", ActorID: "usr_1", Action: "node.write", Target: "nod_" + strconv.Itoa(i%100)}
			if err := s.Write(ctx, func(tx *Tx) error { return tx.InsertAudit(ctx, &e) }); err != nil {
				t.Fatal(err)
			}
		}
		d := time.Since(start)
		t.Logf("5,000 audit inserts %-14s %8s total, %s each", label, d.Round(time.Millisecond), (d / 5000).Round(time.Microsecond))
	}
	insert("with index")
	if _, err := s.w.ExecContext(ctx, `DROP INDEX audit_log_target`); err != nil {
		t.Fatal(err)
	}
	insert("without index")
}
