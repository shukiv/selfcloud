// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnwrittenBytesIgnoresEndedBundles: a cancelled or failed bundle holds no disk. Its end write releases the
// reservation, so a row that still carries one must not count.
func TestUnwrittenBytesIgnoresEndedBundles(t *testing.T) {
	s, ctx := archiveSetup(t)
	rows := []string{
		`INSERT INTO jobs (id, account_id, kind, target, status, created_at, updated_at) VALUES
			('job_c', 'acc_a', 'node.bundle', 'bnd_c', 'succeeded', 0, 0),
			('job_f', 'acc_a', 'node.bundle', 'bnd_f', 'failed', 0, 0)`,
		`INSERT INTO bundle_jobs (id, job_id, account_id, user_id, folder_id, name, name_key, state, reserved_bytes, written_bytes, created_at, updated_at)
			VALUES ('bnd_c', 'job_c', 'acc_a', 'usr_a', 'fld_a', 'c.zip', 'c.zip', 'cancelled', 7000, 0, 0, 0),
			('bnd_f', 'job_f', 'acc_a', 'usr_a', 'fld_a', 'f.zip', 'f.zip', 'failed', 8000, 0, 0, 0)`,
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
	if mine != 0 || total != 0 {
		t.Errorf("UnwrittenBytes(acc_a) = mine %d, total %d; want 0 and 0 for ended bundles", mine, total)
	}
}

// TestUnwrittenBytesCountsPausedImport: a paused import still holds its current entry's reservation until it ends
// (paused imports hold their entry until they end).
func TestUnwrittenBytesCountsPausedImport(t *testing.T) {
	s, ctx := archiveSetup(t)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO import_jobs (id, job_id, account_id, user_id, source, folder_id, on_conflict, state, reserved_bytes, created_at, updated_at)
		VALUES ('imp_p', 'job_p', 'acc_a', 'usr_a', 'archive', 'fld_a', 'rename', 'paused', 400, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	mine, total, err := s.UnwrittenBytes(ctx, "acc_a")
	if err != nil {
		t.Fatal(err)
	}
	if mine != 400 || total != 400 {
		t.Errorf("UnwrittenBytes(acc_a) = mine %d, total %d; want 400 and 400 for a paused import", mine, total)
	}
}

// TestFsckCountsSourceBlob: the blob an import reads through source_blob_id is named, referenced and recorded by the
// check, so a blob the fsck would call unrecorded is reported when the import names it.
func TestFsckCountsSourceBlob(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fsck.db")
	seedMigratedDB(t, path)
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	setup := []string{
		`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_a', 'A', 'active', 0, 0)`,
		`INSERT INTO folders (id, account_id, name, name_key, created_at, updated_at) VALUES ('fld_a', 'acc_a', 'Docs', 'docs', 0, 0)`,
		`INSERT INTO import_jobs (id, job_id, account_id, user_id, source, folder_id, on_conflict, state, source_kind, source_node_id, source_blob_id, created_at, updated_at)
			VALUES ('imp_n', 'job_n', 'acc_a', 'usr_a', 'archive', 'fld_a', 'rename', 'running', 'node', 'nod_x', 'blb_gone', 0, 0)`,
	}
	for _, q := range setup {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCheck(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	snap, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if named, err := snap.BlobNamed(ctx, "blb_gone"); err != nil || !named {
		t.Errorf("BlobNamed(source blob) = %v, %v; want true", named, err)
	}
	if ref, err := snap.BlobReferenced(ctx, "blb_gone"); err != nil || !ref {
		t.Errorf("BlobReferenced(source blob) = %v, %v; want true", ref, err)
	}
	unrecorded, err := snap.UnrecordedBlobsAfter(ctx, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range unrecorded {
		if u.BlobID == "blb_gone" && u.AccountID == "acc_a" {
			found = true
		}
	}
	if !found {
		t.Errorf("UnrecordedBlobsAfter = %+v; want the missing source blob listed", unrecorded)
	}
}

// TestArchiveSumsUseIndexes: the install-wide sums read partial indexes, not the whole table, since bundle_jobs has no
// expiry and grows, so the index must be used. The plan must name the index.
func TestArchiveSumsUseIndexes(t *testing.T) {
	s, ctx := archiveSetup(t)
	plans := map[string]string{
		"bundle_jobs_unwritten":  `EXPLAIN QUERY PLAN SELECT account_id, MAX(reserved_bytes - written_bytes, 0) FROM bundle_jobs WHERE state IN ('pending', 'cancelling')`,
		"import_jobs_unfinished": `EXPLAIN QUERY PLAN SELECT account_id, reserved_bytes FROM import_jobs WHERE state IN ('queued', 'running', 'paused')`,
	}
	for index, q := range plans {
		rows, err := s.r.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		t.Logf("EXPLAIN QUERY PLAN for %s: %v", index, plan)
		if !usesIndex(plan, index) {
			t.Errorf("plan %v does not use %s", plan, index)
		}
	}
}

// usesIndex reports whether a plan step names index exactly: the word after INDEX is the index name, so a
// longer name that starts with it (bundle_jobs_unwritten_x) does not count.
func usesIndex(plan []string, index string) bool {
	for _, detail := range plan {
		fields := strings.Fields(detail)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "INDEX" && fields[i+1] == index {
				return true
			}
		}
	}
	return false
}

// TestArchiveNameLimitIsBytes: a bundle's name is at most 255 bytes, not 255 characters. 128
// accented letters are 256 bytes and are refused.
func TestArchiveNameLimitIsBytes(t *testing.T) {
	s, ctx := archiveSetup(t)
	if _, err := s.w.ExecContext(ctx, `INSERT INTO jobs (id, account_id, kind, target, status, created_at, updated_at)
		VALUES ('job_n', 'acc_a', 'node.bundle', 'bnd_n', 'running', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("é", 128) // 256 bytes
	// Only the name is long: name_key is short, so the refusal below is the name's own rule.
	_, err := s.w.ExecContext(ctx, `INSERT INTO bundle_jobs (id, job_id, account_id, user_id, folder_id, name, name_key, state, created_at, updated_at)
		VALUES ('bnd_n', 'job_n', 'acc_a', 'usr_a', 'fld_a', ?, 'n.zip', 'pending', 0, 0)`, long)
	if err == nil {
		t.Error("a 256-byte bundle name was accepted")
	}
}

// TestImportSourceColumnsAgree: a node source names its node and no upload; an upload source names neither.
func TestImportSourceColumnsAgree(t *testing.T) {
	s, ctx := archiveSetup(t)
	base := `INSERT INTO import_jobs (id, job_id, account_id, user_id, source, folder_id, on_conflict, state, source_kind, source_node_id, created_at, updated_at)
		VALUES (?, 'job_x', 'acc_a', 'usr_a', 'archive', 'fld_a', 'rename', 'queued', ?, ?, 0, 0)`
	cases := map[string][2]string{
		"node source without its node": {"node", ""},
		"upload source naming a node":  {"upload", "nod_x"},
	}
	for name, c := range cases {
		if _, err := s.w.ExecContext(ctx, base, "imp_"+c[0]+c[1], c[0], c[1]); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// TestBundleGoesWithItsFolder: a bundle row of a deleted folder is removed with it, so no pending row is left for a folder
// that is gone.
func TestBundleGoesWithItsFolder(t *testing.T) {
	s, ctx := archiveSetup(t)
	rows := []string{
		`INSERT INTO jobs (id, account_id, kind, target, status, created_at, updated_at) VALUES ('job_g', 'acc_a', 'node.bundle', 'bnd_g', 'running', 0, 0)`,
		`INSERT INTO bundle_jobs (id, job_id, account_id, user_id, folder_id, name, name_key, state, created_at, updated_at)
			VALUES ('bnd_g', 'job_g', 'acc_a', 'usr_a', 'fld_a', 'g.zip', 'g.zip', 'pending', 0, 0)`,
		`DELETE FROM folders WHERE id = 'fld_a'`,
	}
	for _, q := range rows {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, s, `SELECT COUNT(*) FROM bundle_jobs WHERE id = 'bnd_g'`); n != 0 {
		t.Errorf("the bundle of a deleted folder remains: %d rows", n)
	}
}
