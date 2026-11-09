// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// archiveSetup makes two accounts, acc_a and acc_b, with a folder each. The job rows the bundles need are made by
// the caller.
func archiveSetup(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s := openTest(t)
	setup := []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_b', 'B', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_b', 'acc_b', 'Docs', 'docs', 0, 0)`,
	}
	for _, q := range setup {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

// TestUnwrittenBytesCountsEveryReservation: the disk rule counts what the unfinished writers still hold. A pending
// bundle counts its reservation less what it has written; a cancelling bundle counts too, since it holds its
// reservation until its cancel write runs; an unfinished import counts its current entry; a pending stored zip counts
// its reserved bytes. Finished work does not count: a ready zip, a done import and a ready bundle are on disk already.
func TestUnwrittenBytesCountsEveryReservation(t *testing.T) {
	s, ctx := archiveSetup(t)
	rows := []string{
		`INSERT INTO jobs (id, account_id, kind, target, status, created_at, updated_at) VALUES
			('job_b1', 'acc_a', 'node.bundle', 'bnd_1', 'running', 0, 0),
			('job_b2', 'acc_a', 'node.bundle', 'bnd_2', 'running', 0, 0),
			('job_b3', 'acc_a', 'node.bundle', 'bnd_3', 'succeeded', 0, 0),
			('job_b4', 'acc_b', 'node.bundle', 'bnd_4', 'running', 0, 0)`,
		// pending: 1000 reserved, 200 written, so 800 is unwritten. cancelling: 500, none written. ready: never counts.
		`INSERT INTO bundle_jobs (id, job_id, account_id, user_id, folder_id, name, name_key, state, reserved_bytes, written_bytes, created_at, updated_at)
			VALUES ('bnd_1', 'job_b1', 'acc_a', 'usr_a', 'fld_a', 'one.zip', 'one.zip', 'pending', 1000, 200, 0, 0),
			('bnd_2', 'job_b2', 'acc_a', 'usr_a', 'fld_a', 'two.zip', 'two.zip', 'cancelling', 500, 0, 0, 0),
			('bnd_3', 'job_b3', 'acc_a', 'usr_a', 'fld_a', 'three.zip', 'three.zip', 'ready', 9000, 0, 0, 0),
			('bnd_4', 'job_b4', 'acc_b', 'usr_b', 'fld_b', 'four.zip', 'four.zip', 'pending', 40, 0, 0, 0)`,
		// queued with 300 in its current entry; running with 0; done with a stale 9999 that must not count.
		`INSERT INTO import_jobs (id, job_id, account_id, user_id, source, folder_id, on_conflict, state, reserved_bytes, created_at, updated_at)
			VALUES ('imp_1', 'job_i1', 'acc_a', 'usr_a', 'archive', 'fld_a', 'rename', 'queued', 300, 0, 0),
			('imp_2', 'job_i2', 'acc_a', 'usr_a', 'archive', 'fld_a', 'rename', 'running', 0, 0, 0),
			('imp_3', 'job_i3', 'acc_a', 'usr_a', 'archive', 'fld_a', 'rename', 'done', 9999, 0, 0)`,
		// pending: 4000 reserved. ready: 7000 on disk, not counted. The account-b pending zip counts only in total.
		`INSERT INTO zips (id, account_id, created_by, created_kind, state, reserved, size, created_at)
			VALUES ('zip_1', 'acc_a', 'usr_a', 'user', 'pending', 4000, 0, 0),
			('zip_2', 'acc_a', 'usr_a', 'user', 'ready', 0, 7000, 0),
			('zip_3', 'acc_b', 'usr_b', 'user', 'pending', 10, 0, 0)`,
	}
	for _, q := range rows {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	mine, total, err := s.UnwrittenBytes(ctx, "acc_a")
	if err != nil {
		t.Fatal(err)
	}
	// acc_a: 800 (bundle) + 500 (cancelling bundle) + 300 (import) + 4000 (zip) = 5600. total adds acc_b's 40 and 10.
	if mine != 5600 || total != 5650 {
		t.Errorf("UnwrittenBytes(acc_a) = mine %d, total %d; want 5600 and 5650", mine, total)
	}
}

// TestArchiveMigrationsApplyOnAFreshStore: the bundle tables and the import's source columns exist after Open, and the
// import's source columns default to an upload, so the rows that exist before the change read as uploads.
func TestArchiveMigrationsApplyOnAFreshStore(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, table := range []string{"bundle_jobs", "bundle_items", "bundle_roots"} {
		var n int
		if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: count %d, %v; want 1", table, n, err)
		}
	}
	cols := map[string]string{}
	rows, err := s.r.QueryContext(ctx, `SELECT name, dflt_value FROM pragma_table_info('import_jobs')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		var dflt *string
		if err := rows.Scan(&name, &dflt); err != nil {
			t.Fatal(err)
		}
		if dflt != nil {
			cols[name] = *dflt
		} else {
			cols[name] = ""
		}
	}
	rows.Close()
	for _, c := range []string{"source_kind", "source_node_id", "source_blob_id"} {
		if _, ok := cols[c]; !ok {
			t.Errorf("import_jobs has no column %s", c)
		}
	}
	if cols["source_kind"] != "'upload'" {
		t.Errorf("source_kind default = %q, want 'upload'", cols["source_kind"])
	}
}

// TestNodeSourceBlobIsPinned: a drive file an import reads is held by the import's source_blob_id, so the blob collector
// does not orphan it while the import can still read it (decision 120). The blob is live, with no node naming it.
func TestNodeSourceBlobIsPinned(t *testing.T) {
	s, ctx := archiveSetup(t)
	rows := []string{
		`INSERT INTO blobs (id, account_id, size, sha256, state, created_at) VALUES ('blb_src', 'acc_a', 3, '00', 'live', 0)`,
		`INSERT INTO import_jobs (id, job_id, account_id, user_id, source, folder_id, on_conflict, state, source_kind, source_node_id, source_blob_id, created_at, updated_at)
			VALUES ('imp_n', 'job_n', 'acc_a', 'usr_a', 'archive', 'fld_a', 'rename', 'running', 'node', 'nod_x', 'blb_src', 0, 0)`,
	}
	for _, q := range rows {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		_, err := tx.OrphanUnreferenced(ctx, Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.r.QueryRowContext(ctx, `SELECT state FROM blobs WHERE id = 'blb_src'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "live" {
		t.Errorf("the source blob of a running import is %q, want live", state)
	}
}

// migrationNumber is the leading number of a migration file name.
var migrationNumber = regexp.MustCompile(`^(\d{4})_`)

// TestMigrationsForwardOnlyAdditive: the archive migrations (0050 on) only create tables and add columns. Nothing is
// dropped or renamed, so a binary that predates them still reads the rows it knows (decision 120).
func TestMigrationsForwardOnlyAdditive(t *testing.T) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		m := migrationNumber.FindStringSubmatch(e.Name())
		if m == nil || m[1] < "0050" {
			continue
		}
		checked++
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		up := strings.ToUpper(string(b))
		for _, bad := range []string{"DROP ", "RENAME ", "ALTER COLUMN", "DELETE FROM"} {
			if strings.Contains(up, bad) {
				t.Errorf("%s: contains %q; archive migrations are additive", e.Name(), bad)
			}
		}
	}
	if checked < 2 {
		t.Fatalf("found %d archive migrations, want at least 2", checked)
	}
}
