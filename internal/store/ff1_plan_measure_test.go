// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestFF1FilterPlansAndTimings is the Phase 0b measurement of slice ff1 . It runs only when
// SELFCLOUD_FF1_MEASURE is set, and it is not committed.
//
// It seeds one folder of children (SELFCLOUD_FF1_ROWS, default 200,000). Names carry an extension for most files, and the
// media type is set independently, so the extension-first rule has work to do: some names disagree with their type.
// It then measures, with the full group CASE and the extension-first rule (the same table the Go function uses):
//   - the backfill of type_group as one UPDATE over every node (the migration's cost);
//   - that the SQL backfill equals the Go function on every row (mismatches must be 0);
//   - the write cost of a statement that inserts 2,000 files and one that renames 2,000 files, before the candidate
//     indexes exist and after they are built (the index maintenance cost, which is the part that depends on the design);
//   - the plans and times of the list queries, before and after the indexes, including the trash.
//
// Writes are measured as single statements, not through InsertNode or UpdateNodeHeld, so the cost is the index
// maintenance and row write, not the store's search and audit work.
func TestFF1FilterPlansAndTimings(t *testing.T) {
	if os.Getenv("SELFCLOUD_FF1_MEASURE") == "" {
		t.Skip("set SELFCLOUD_FF1_MEASURE to run the ff1 measurement")
	}
	rows := 200000
	if v := os.Getenv("SELFCLOUD_FF1_ROWS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		rows = n
	}
	const page = 26
	s := openTest(t)
	ctx := t.Context()

	// The pragmas the store sets or relies on, as the measured connection sees them (added for the first measurement).
	for _, p := range []string{"journal_mode", "wal_autocheckpoint", "cache_size", "synchronous", "page_size"} {
		var v any
		if err := s.w.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); err != nil {
			t.Fatal(err)
		}
		t.Logf("PRAGMA %s = %v", p, v)
	}
	// The database file, so the write-ahead log's size can be logged around each write (added for the first measurement).
	var dbSeq int
	var dbName, dbFile string
	if err := s.r.QueryRowContext(ctx, "PRAGMA database_list").Scan(&dbSeq, &dbName, &dbFile); err != nil {
		t.Fatal(err)
	}
	walSize := func() int64 {
		fi, err := os.Stat(dbFile + "-wal")
		if err != nil {
			return -1
		}
		return fi.Size()
	}
	exec := func(what, q string, args ...any) time.Duration {
		t.Helper()
		start := time.Now()
		if _, err := s.w.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		d := time.Since(start)
		t.Logf("%s in %v (wal %d bytes)", what, d.Round(time.Microsecond), walSize())
		return d
	}

	// The group table (design section 3, with review 's fixes). Extensions decide first; then the media type.
	type group struct {
		id       string
		exts     []string
		mimes    []string
		prefixes []string
	}
	groups := []group{
		{"wordprocessing", []string{"docx", "doc", "odt", "rtf", "txt", "md"},
			[]string{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/msword",
				"application/vnd.oasis.opendocument.text", "application/rtf", "text/plain", "text/markdown"}, nil},
		{"spreadsheet", []string{"xlsx", "xls", "ods", "csv"},
			[]string{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.ms-excel",
				"application/vnd.oasis.opendocument.spreadsheet", "text/csv"}, nil},
		{"presentation", []string{"pptx", "ppt", "odp"},
			[]string{"application/vnd.openxmlformats-officedocument.presentationml.presentation",
				"application/vnd.ms-powerpoint", "application/vnd.oasis.opendocument.presentation"}, nil},
		{"drawing", []string{"odg"}, []string{"application/vnd.oasis.opendocument.graphics"}, nil},
		{"pdf", []string{"pdf"}, []string{"application/pdf"}, nil},
		{"image", []string{"jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "heic", "heif", "bmp", "tif", "tiff"},
			[]string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/avif", "image/svg+xml", "image/heic",
				"image/heif", "image/bmp", "image/tiff"}, nil},
		{"video", []string{"mp4", "mov", "webm", "mkv", "avi", "ogv"}, nil, []string{"video/"}},
		{"audio", []string{"mp3", "m4a", "ogg", "oga", "flac", "wav"}, nil, []string{"audio/"}},
		{"archive", []string{"zip", "tar", "gz", "7z", "rar"},
			[]string{"application/zip", "application/x-zip-compressed", "application/x-tar", "application/gzip",
				"application/x-7z-compressed", "application/x-rar-compressed"}, nil},
	}
	extGroup := map[string]string{}
	mimeGroup := map[string]string{}
	for _, g := range groups {
		for _, e := range g.exts {
			extGroup[e] = g.id
		}
		for _, m := range g.mimes {
			mimeGroup[m] = g.id
		}
	}
	// groupOf is the Go rule: a file's extension decides when it is known; otherwise its media type (parameters
	// dropped, lower case) and then its prefix. A directory or a shortcut has no group.
	groupOf := func(kind, name, mime string) string {
		if kind != "file" {
			return ""
		}
		if i := strings.LastIndexByte(name, '.'); i >= 0 && i < len(name)-1 {
			if g, ok := extGroup[strings.ToLower(name[i+1:])]; ok {
				return g
			}
		}
		base := strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
		if g, ok := mimeGroup[base]; ok {
			return g
		}
		for _, g := range groups {
			for _, p := range g.prefixes {
				if strings.HasPrefix(base, p) {
					return g.id
				}
			}
		}
		return ""
	}

	// groupCaseSQL is the same rule as one CASE expression over the columns named with prefix pre ("n." or "nodes.").
	// The extension is the text after the last dot, found with rtrim: the set of rtrim is the name without its dots, so
	// it strips the characters after the last dot and stops at it.
	groupCaseSQL := func(pre string) string {
		ext := fmt.Sprintf("lower(CASE WHEN instr(%[1]sname, '.') > 0 THEN substr(%[1]sname, length(rtrim(%[1]sname, replace(%[1]sname, '.', ''))) + 1) ELSE '' END)", pre)
		mime := strings.ReplaceAll(baseMimeSQL, "n.mime_type", pre+"mime_type")
		var b strings.Builder
		b.WriteString("CASE WHEN " + pre + "kind <> 'file' THEN '' ")
		for _, g := range groups {
			if len(g.exts) > 0 {
				b.WriteString("WHEN " + ext + " IN ('" + strings.Join(g.exts, "', '") + "') THEN '" + g.id + "' ")
			}
		}
		for _, g := range groups {
			if len(g.mimes) > 0 {
				b.WriteString("WHEN " + mime + " IN ('" + strings.Join(g.mimes, "', '") + "') THEN '" + g.id + "' ")
			}
			for _, p := range g.prefixes {
				b.WriteString("WHEN " + mime + " LIKE '" + p + "%' THEN '" + g.id + "' ")
			}
		}
		b.WriteString("ELSE '' END")
		return b.String()
	}

	// The fixture: names carry an extension for 9 in 10 files (one in ten has none), and the media type comes from a
	// band of i % 100 that does not follow the name, so about one file in three has a name and type that disagree.
	type band struct {
		below int
		mime  string
	}
	bands := []band{
		{10, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{15, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{18, "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{23, "application/pdf"},
		{43, "image/jpeg"},
		{46, "video/mp4"},
		{48, "audio/mpeg"},
		{49, "application/zip"},
		{50, "text/plain; charset=utf-8"},
		{100, "application/octet-stream"},
	}
	var mimeCase strings.Builder
	mimeCase.WriteString("CASE ")
	for _, b := range bands {
		fmt.Fprintf(&mimeCase, "WHEN (i %% 100) < %d THEN '%s' ", b.below, b.mime)
	}
	mimeCase.WriteString("END")
	exts := []string{"docx", "pdf", "jpg", "webm", "ogg", "xlsx", "", "txt", "zip", "bin"}
	var extCase strings.Builder
	extCase.WriteString("CASE (i % 10) ")
	for k, e := range exts {
		if e == "" {
			fmt.Fprintf(&extCase, "WHEN %d THEN '' ", k)
		} else {
			fmt.Fprintf(&extCase, "WHEN %d THEN '.%s' ", k, e)
		}
	}
	extCase.WriteString("END")

	exec("account, folder, root, user", `INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_f', 'F', 'active', 0, 0);
		INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_f', 'acc_f', 'Docs', 'docs', 0, 0);
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, created_at, modified_at)
			VALUES ('nod_root', 'acc_f', 'fld_f', NULL, '', '', 'dir', 0, 0);
		INSERT INTO users (id, account_id, email, email_key, role, status, created_at, updated_at)
			VALUES ('usr_f', 'acc_f', 'f@test.invalid', 'f@test.invalid', 'member', 'active', 0, 0)`)
	exec(fmt.Sprintf("%d children", rows), fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d)
		INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at)
		SELECT printf('nod_%%06d', i), 'acc_f', 'fld_f', 'nod_root',
			'file ' || i || %s,
			printf('k%%06d', i),
			CASE WHEN i %% 50 = 0 THEN 'dir' ELSE 'file' END,
			CASE WHEN i %% 50 = 0 THEN 0 ELSE (i * 7919) %% 4096 END,
			CASE WHEN i %% 50 = 0 THEN '' ELSE %s END,
			0, 1700000000000 + ((i * 7919) %% 1000) * 86400000
		FROM n`, rows, extCase.String(), mimeCase.String()))
	exec("trash 1 in 100 files", `UPDATE nodes SET trashed_at = 1700000000000 + (rowid % 500) * 1000
		WHERE kind = 'file' AND rowid % 100 = 0`)
	exec("recent and stars, 1000 each", `INSERT INTO recent (user_id, node_id, account_id, viewed_at, edited_at, last_at)
		SELECT 'usr_f', id, 'acc_f', created_at, NULL, 1700000000000 + (rowid % 997) * 1000
		FROM nodes WHERE kind = 'file' AND trashed_at IS NULL AND rowid <= 1000;
		INSERT INTO stars (user_id, node_id, account_id, created_at)
		SELECT 'usr_f', id, 'acc_f', 1700000000000 + (rowid % 991) * 1000
		FROM nodes WHERE kind = 'file' AND trashed_at IS NULL AND rowid <= 1000`)

	// openTest has run every migration, so the group indexes already exist. The measurement starts from today's indexes:
	// they are dropped here and built again below, as the candidates, and their build times are logged.
	for _, name := range []string{"nodes_list_name_group", "nodes_list_modified_group", "nodes_list_size_group",
		"nodes_size_group", "nodes_size_account_group", "nodes_trash_group"} {
		exec("drop "+name, "DROP INDEX "+name)
	}

	// The backfill: the column and one UPDATE over every node (the migration's shape).
	// The column is added by migration 0053, which openTest has already run, so it is not added here (the first run
	// failed on the duplicate column). The statement below is the migration's backfill.
	backfill := exec("BACKFILL type_group, one UPDATE over all nodes", `UPDATE nodes SET type_group = `+groupCaseSQL("nodes."))

	// Equivalence: the SQL backfill against the Go rule, on every row.
	mismatch := 0
	counts := map[string]int{}
	erows, err := s.r.QueryContext(ctx, `SELECT kind, name, mime_type, type_group FROM nodes`)
	if err != nil {
		t.Fatal(err)
	}
	for erows.Next() {
		var kind, name, mime, got string
		if err := erows.Scan(&kind, &name, &mime, &got); err != nil {
			t.Fatal(err)
		}
		want := groupOf(kind, name, mime)
		counts[got]++
		if want != got {
			mismatch++
			if mismatch <= 5 {
				t.Errorf("row %q %q: SQL %q, Go %q", kind, name, got, want)
			}
		}
	}
	erows.Close()
	t.Logf("SQL backfill vs Go rule: %d mismatches over %d rows; counts by group: %v", mismatch, rows, counts)

	// Plans and times.
	measure := func(name, q string, args ...any) {
		t.Helper()
		var plan []string
		prows, err := s.r.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatalf("%s: plan: %v", name, err)
		}
		for prows.Next() {
			var id, parent, unused int
			var detail string
			if err := prows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		prows.Close()
		best := time.Duration(1<<63 - 1)
		got := 0
		for i := 0; i < 3; i++ {
			start := time.Now()
			rs, err := s.r.QueryContext(ctx, q, args...)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			n := 0
			for rs.Next() {
				var id string
				if err := rs.Scan(&id); err != nil {
					t.Fatal(err)
				}
				n++
			}
			if err := rs.Err(); err != nil {
				t.Fatal(err)
			}
			rs.Close()
			if d := time.Since(start); d < best {
				best = d
			}
			got = n
		}
		t.Logf("%-42s rows=%2d best=%-12v plan: %s", name, got, best.Round(time.Microsecond), strings.Join(plan, " | "))
	}
	kids := []any{"acc_f", "nod_root"}
	base := "SELECT n.id FROM nodes n WHERE n.account_id = ? AND n.parent_id = ? AND n.trashed_at IS NULL AND n.kind = 'file'"
	nameOrder := " ORDER BY n.name COLLATE " + NameCollation + ", n.id LIMIT ?"
	lo := int64(1700000000000 + 100*86400000)
	hi := lo + 100*86400000
	argsK := func(extra ...any) []any { return append(append([]any{}, kids...), extra...) }
	largest := "SELECT n.id FROM nodes n JOIN folders f ON f.id = n.folder_id AND f.staged = 0 WHERE n.account_id = ? AND n.kind = 'file' AND n.folder_id = ?"
	largestOrder := " ORDER BY n.size DESC, n.id DESC LIMIT ?"
	fl := []any{"acc_f", "fld_f"}

	// Writes: a statement that inserts 2,000 files and one that renames 2,000 files, each in five rounds.
	writes := func(label string, gen int) {
		for r := 0; r < 5; r++ {
			exec(fmt.Sprintf("%s insert 2000, round %d", label, r), fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2000)
				INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at, type_group)
				SELECT printf('nodw%d_%d_%%06d', i), 'acc_f', 'fld_f', 'nod_root', printf('new %%06d.docx', i), printf('w%d_%d_%%06d', i),
					'file', 10, 'application/octet-stream', 0, 1700000000000, 'wordprocessing' FROM n`, gen, r, gen, r))
			exec(fmt.Sprintf("%s rename 2000, round %d", label, r), fmt.Sprintf(`UPDATE nodes
				SET name = printf('ren %%06d.xlsx', rowid), name_key = printf('q%d_%%06d', rowid), type_group = 'spreadsheet',
					modified_at = modified_at + 1
				WHERE folder_id = 'fld_f' AND kind = 'file' AND rowid > %d AND rowid <= %d`, r, r*2000, r*2000+2000))
		}
	}
	countIndexes := func(label string) {
		var n int
		irows, err := s.r.QueryContext(ctx, "PRAGMA index_list('nodes')")
		if err != nil {
			t.Fatal(err)
		}
		for irows.Next() {
			var seq, unique, partial int
			var name, origin string
			if err := irows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				t.Fatal(err)
			}
			n++
		}
		irows.Close()
		t.Logf("%s: %d indexes on nodes (including the primary key's)", label, n)
	}

	countIndexes("before candidates")
	t.Log("== writes before the candidate indexes")
	writes("W0", 0)

	t.Log("== set A: today's indexes, plus the group column")
	measure("A1 children name, no filter", base+nameOrder, argsK(page)...)
	measure("A2 children name, group wordprocessing (residual)", base+" AND "+groupCaseSQL("n.")+" = ?"+nameOrder, argsK("wordprocessing", page)...)
	measure("A5 children name, modified 10%", base+" AND n.modified_at >= ? AND n.modified_at < ?"+nameOrder, argsK(lo, hi, page)...)
	measure("A10 largest, none", largest+largestOrder, append(append([]any{}, fl...), page)...)
	measure("A13 trash, group wordprocessing", "SELECT n.id FROM nodes n WHERE n.account_id = ? AND n.trashed_at IS NOT NULL AND "+groupCaseSQL("n.")+" = ? ORDER BY n.trashed_at DESC, n.id DESC LIMIT ?", "acc_f", "wordprocessing", page)

	// Candidate indexes: built, timed, counted.
	for _, ix := range []string{
		`CREATE INDEX nodes_list_name_group ON nodes (parent_id, kind, type_group, name COLLATE selfcloud_name, id) WHERE trashed_at IS NULL`,
		`CREATE INDEX nodes_list_modified_group ON nodes (parent_id, kind, type_group, modified_at, id) WHERE trashed_at IS NULL`,
		`CREATE INDEX nodes_list_size_group ON nodes (parent_id, kind, type_group, size, id) WHERE trashed_at IS NULL`,
		`CREATE INDEX nodes_size_group ON nodes (folder_id, type_group, size DESC, id DESC) WHERE kind = 'file'`,
		`CREATE INDEX nodes_size_account_group ON nodes (account_id, type_group, size DESC, id DESC) WHERE kind = 'file'`,
		`CREATE INDEX nodes_trash_group ON nodes (account_id, type_group, trashed_at, id) WHERE trashed_at IS NOT NULL`,
	} {
		exec("build "+ix[len("CREATE INDEX "):strings.Index(ix, " ON")], ix)
	}
	countIndexes("after candidates")
	t.Log("== writes after the candidate indexes")
	writes("W1", 1)

	t.Log("== set B: with the candidate indexes")
	measure("B1 children name, group wordprocessing (=)", base+" AND n.type_group = ?"+nameOrder, argsK("wordprocessing", page)...)
	measure("B2 children name, group doc or archive (IN)", base+" AND n.type_group IN (?, ?)"+nameOrder, argsK("wordprocessing", "archive", page)...)
	measure("B3 children name, group archive (rare)", base+" AND n.type_group = ?"+nameOrder, argsK("archive", page)...)
	measure("B4 children name, group none", base+" AND n.type_group = ?"+nameOrder, argsK("nothing", page)...)
	measure("B5 children name, group + modified 10%", base+" AND n.type_group = ? AND n.modified_at >= ? AND n.modified_at < ?"+nameOrder, argsK("wordprocessing", lo, hi, page)...)
	measure("B6 children modified sort, group", base+" AND n.type_group = ? ORDER BY n.modified_at, n.id LIMIT ?", argsK("wordprocessing", page)...)
	measure("B8 children size sort, group", base+" AND n.type_group = ? ORDER BY n.size, n.id LIMIT ?", argsK("wordprocessing", page)...)
	measure("B9 largest, group wordprocessing", largest+" AND n.type_group = ?"+largestOrder, append(append([]any{}, fl...), "wordprocessing", page)...)
	measure("B10 largest, group archive (rare)", largest+" AND n.type_group = ?"+largestOrder, append(append([]any{}, fl...), "archive", page)...)
	measure("B11 trash, group wordprocessing", "SELECT n.id FROM nodes n WHERE n.account_id = ? AND n.trashed_at IS NOT NULL AND n.type_group = ? ORDER BY n.trashed_at DESC, n.id DESC LIMIT ?", "acc_f", "wordprocessing", page)
	measure("B12 trash, group archive (rare)", "SELECT n.id FROM nodes n WHERE n.account_id = ? AND n.trashed_at IS NOT NULL AND n.type_group = ? ORDER BY n.trashed_at DESC, n.id DESC LIMIT ?", "acc_f", "archive", page)
	_ = backfill
}
