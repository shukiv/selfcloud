// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Measurements of the text policy (review S2, S3, S5). Gated by SELFCLOUD_MEASURE_CONTENT=1. The data goes under /var/tmp,
// as the brief asks, and nothing here runs in the gate. The figures are in decision 46.

// measureStore opens a store under /var/tmp with two accounts, for a measurement.
func measureStore(t *testing.T) *Store {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "q3b-measure-")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(dir, "m.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	t.Logf("measurement data: %s", dir)
	for _, acc := range []string{"acc_big", "acc_small"} {
		if _, err := s.w.ExecContext(ctx, `INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES (?, ?, 'active', 0, 0)`, acc, acc); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// randomWords returns n words of six random lowercase letters, separated by spaces: high-entropy text, so every word is a
// new entry in the index.
func randomWords(r *rand.Rand, n int, prefix string) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteByte(' ')
		}
		if prefix != "" && i%7 == 0 {
			b.WriteString(prefix)
		}
		for range 6 {
			b.WriteByte(byte('a' + r.IntN(26)))
		}
	}
	return b.String()
}

// addReadyText writes a blob, its content row and its full-text row, as the worker would leave them for a file, in one
// write. The usage row is not set: the measurement does not check caps.
func addReadyText(t *testing.T, s *Store, acc, blob, text string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, `INSERT INTO blobs (id, account_id, size, sha256, state, created_at)
		VALUES (?, ?, ?, 'x', 'live', 0)`, blob, acc, len(text)); err != nil {
		t.Fatal(err)
	}
	res, err := s.w.ExecContext(ctx, `INSERT INTO search_texts (blob_id, account_id, state, source, bytes)
		VALUES (?, ?, 'ready', 'plain', ?)`, blob, acc, len(text))
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, "INSERT INTO search_text_fts (rowid, text, account_id) VALUES (?, ?, ?)", id, text, acc); err != nil {
		t.Fatal(err)
	}
}

// fileSize checkpoints the log and returns the database file's size.
func fileSize(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := s.w.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	var size int64
	if err := s.r.QueryRowContext(ctx, "SELECT (SELECT page_count FROM pragma_page_count()) * (SELECT page_size FROM pragma_page_size())").Scan(&size); err != nil {
		t.Fatal(err)
	}
	return size
}

// TestMeasureSecureDeleteCost (review S2): the write cost of secure delete, measured with it on (the default) and off. A
// batch of 2,000 files of 1 KiB each is written, then deleted one write at a time, as the sweep deletes them.
func TestMeasureSecureDeleteCost(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to measure the cost of secure delete")
	}
	// The two mechanisms are measured apart: the page pragma and FTS5's own option (each on, then off).
	for _, mode := range []struct {
		name        string
		pragma, fts bool
	}{{"pragma and FTS5 on (shipped)", true, true}, {"pragma on, FTS5 off", true, false}, {"pragma off, FTS5 on", false, true}, {"both off", false, false}} {
		s := measureStore(t)
		ctx := context.Background()
		pragma := 0
		if mode.pragma {
			pragma = 1
		}
		if _, err := s.w.ExecContext(ctx, fmt.Sprintf("PRAGMA secure_delete = %d", pragma)); err != nil {
			t.Fatal(err)
		}
		ftsOpt := 0
		if mode.fts {
			ftsOpt = 1
		}
		if _, err := s.w.ExecContext(ctx, fmt.Sprintf("INSERT INTO search_text_fts(search_text_fts, rank) VALUES ('secure-delete', %d)", ftsOpt)); err != nil {
			t.Fatal(err)
		}
		r := rand.New(rand.NewPCG(1, 2))
		const files = 2000
		start := time.Now()
		for i := range files {
			addReadyText(t, s, "acc_big", fmt.Sprintf("blb_s%d", i), randomWords(r, 170, ""))
		}
		written := time.Since(start)
		size0 := fileSize(t, s)
		start = time.Now()
		for i := range files {
			if err := s.Write(ctx, func(tx *Tx) error {
				return tx.dropText(ctx, "acc_big", fmt.Sprintf("blb_s%d", i))
			}); err != nil {
				t.Fatal(err)
			}
		}
		deleted := time.Since(start)
		size1 := fileSize(t, s)
		t.Logf("%s: %d files of 1 KiB written in %v (%v each); deleted one write each in %v (%v each); file %d -> %d bytes",
			mode.name, files, written.Round(time.Millisecond), (written / files).Round(time.Microsecond),
			deleted.Round(time.Millisecond), (deleted / files).Round(time.Microsecond), size0, size1)
	}
}

// TestMeasureTextGrowth (review S3): the database's growth for high-entropy text, per MiB of extracted text. Four files of
// 1 MiB of random words each are written as ready text.
func TestMeasureTextGrowth(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to measure the growth of stored text")
	}
	s := measureStore(t)
	before := fileSize(t, s)
	r := rand.New(rand.NewPCG(3, 4))
	var text int64
	start := time.Now()
	for i := range 4 {
		body := randomWords(r, (1<<20)/7, "")
		text += int64(len(body))
		addReadyText(t, s, "acc_big", fmt.Sprintf("blb_g%d", i), body)
	}
	after := fileSize(t, s)
	t.Logf("growth: %d bytes of random-word text written in %v; the database grew by %d bytes: %.2f bytes on disk per byte of text",
		text, time.Since(start).Round(time.Millisecond), after-before, float64(after-before)/float64(text))
}

// TestMeasurePrefixAcrossTenants (review S5): a short prefix query on a small account, while a large other tenant's corpus
// is in the same full-text table. 150,000 rows of 20 words are written for the big tenant, 10 for the small one.
func TestMeasurePrefixAcrossTenants(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to measure prefix queries across tenants")
	}
	s := measureStore(t)
	r := rand.New(rand.NewPCG(5, 6))
	const big = 150000
	ctx := context.Background()
	// The small tenant's rows first: the writer is one connection, so they cannot be written inside the big transaction.
	for i := range 10 {
		addReadyText(t, s, "acc_small", fmt.Sprintf("blb_q%d", i), "ab"+strings.Repeat("z", i)+" small tenant words")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range big {
		body := randomWords(r, 20, "")
		if _, err := tx.ExecContext(ctx, `INSERT INTO blobs (id, account_id, size, sha256, state, created_at) VALUES (?, 'acc_big', 100, 'x', 'live', 0)`, fmt.Sprintf("blb_p%d", i)); err != nil {
			t.Fatal(err)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO search_texts (blob_id, account_id, state, source, bytes) VALUES (?, 'acc_big', 'ready', 'plain', 100)`, fmt.Sprintf("blb_p%d", i))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if _, err := tx.ExecContext(ctx, "INSERT INTO search_text_fts (rowid, text, account_id) VALUES (?, ?, 'acc_big')", id, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct{ name, match, acc string }{
		{"prefix ab, small tenant", `"ab"*`, "acc_small"},
		{"prefix ab, big tenant (its own corpus)", `"ab"*`, "acc_big"},
		{"prefix abc, small tenant", `"abc"*`, "acc_small"},
	} {
		start := time.Now()
		var n int
		rows, err := s.r.QueryContext(ctx, `SELECT rowid FROM search_text_fts WHERE search_text_fts MATCH ? AND account_id = ? LIMIT 50`, q.match, q.acc)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			n++
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s over %d rows of the big tenant: %d hits in %v", q.name, big, n, time.Since(start).Round(time.Microsecond))
	}
}

// TestMeasurePrefixIndex (review S5): the same 60,000 rows in a full-text table with no prefix index and one with prefix='3'.
// The index's size is the file's growth, and each query is timed on both tables. Gated by SELFCLOUD_MEASURE_CONTENT=1.
// The shipped table has no prefix index; the decision (decision 46) says whether this one is adopted.
func TestMeasurePrefixIndex(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_CONTENT") != "1" {
		t.Skip("set SELFCLOUD_MEASURE_CONTENT=1 to measure the prefix index")
	}
	s := measureStore(t)
	ctx := context.Background()
	for _, ddl := range []string{
		"CREATE VIRTUAL TABLE zf_plain USING fts5(text, account_id, tokenize='unicode61 remove_diacritics 2')",
		"CREATE VIRTUAL TABLE zf_p3 USING fts5(text, account_id, tokenize='unicode61 remove_diacritics 2', prefix='3')",
	} {
		if _, err := s.w.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	before := fileSize(t, s)
	const big, small = 59000, 1000
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range big + small {
		acc := "acc_big"
		if i >= big {
			acc = "acc_small"
		}
		text := fmt.Sprintf("abacus alpha beta word%d gamma delta", i)
		for _, tbl := range []string{"zf_plain", "zf_p3"} {
			if _, err := tx.ExecContext(ctx, "INSERT INTO "+tbl+" (rowid, text, account_id) VALUES (?, ?, ?)", i+1, text, acc); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after := fileSize(t, s)
	t.Logf("both tables, %d rows each: the file grew %d bytes (both indexes together, no other table written)", big+small, after-before)
	for _, tbl := range []string{"zf_plain", "zf_p3"} {
		var pages int64
		if err := s.r.QueryRowContext(ctx, "SELECT COALESCE(SUM(pgsize), 0) FROM dbstat WHERE name LIKE ?", tbl+"%").Scan(&pages); err != nil {
			t.Logf("dbstat is not available: %v", err)
			break
		}
		t.Logf("%s: %d bytes of pages (dbstat, all its shadow tables)", tbl, pages)
	}
	for _, q := range []struct{ name, tbl, match, acc string }{
		{"3-rune prefix alp, big tenant, plain", "zf_plain", `"alp"*`, "acc_big"},
		{"3-rune prefix alp, big tenant, prefix='3'", "zf_p3", `"alp"*`, "acc_big"},
		{"3-rune prefix aba, small tenant, plain", "zf_plain", `"aba"*`, "acc_small"},
		{"3-rune prefix aba, small tenant, prefix='3'", "zf_p3", `"aba"*`, "acc_small"},
		{"2-rune prefix ab, small tenant, plain (expands)", "zf_plain", `"ab"*`, "acc_small"},
		{"2-rune prefix ab, small tenant, prefix='3' (expands)", "zf_p3", `"ab"*`, "acc_small"},
		{"2-rune exact ab, small tenant, plain (no expansion)", "zf_plain", `"ab"`, "acc_small"},
	} {
		start := time.Now()
		var n int
		rows, err := s.r.QueryContext(ctx, "SELECT rowid FROM "+q.tbl+" WHERE "+q.tbl+" MATCH ? AND account_id = ? LIMIT 100", q.match, q.acc)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			n++
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d hits in %v", q.name, n, time.Since(start).Round(time.Microsecond))
	}
}
