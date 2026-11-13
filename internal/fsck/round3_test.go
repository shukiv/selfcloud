// SPDX-License-Identifier: AGPL-3.0-or-later

package fsck

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// A cancelled run is not a damaged database: it is an error (exit 2), with integrity still unchecked and no
// sqlite_integrity finding. Mutant: every failure at the start is a finding, as in.
func TestCancelledRunIsNotADamagedDatabase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := Run(ctx, Options{DataDir: f.dir, DBPath: f.dbPath, MinAge: time.Hour})
	if err == nil {
		t.Fatalf("a cancelled run returned no error (integrity %q, findings %+v)", rep.Integrity, rep.Findings)
	}
	if rep.Integrity != "unchecked" {
		t.Fatalf("integrity = %q after a cancelled run, want unchecked", rep.Integrity)
	}
	if got := find(rep, KindIntegrity); len(got) != 0 {
		t.Fatalf("a cancelled run reported a damaged database: %+v", got)
	}
}

// A database another connection holds exclusively is busy, not damaged: the run is an error (exit 2), with no
// sqlite_integrity finding and integrity unchecked. Mutant: as for the cancelled run.
func TestBusyDatabaseIsNotADamagedDatabase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// The fixture's own connections hold the database's shared memory; they are closed so the holder can lock it.
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	holder, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { holder.Close() })
	conn, err := holder.Conn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// EXCLUSIVE locking mode in WAL: the holder keeps the database's shared memory, so another connection cannot open it.
	if _, err := conn.ExecContext(f.ctx, "PRAGMA locking_mode=EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM blobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(f.ctx, Options{DataDir: f.dir, DBPath: f.dbPath, MinAge: time.Hour, busyMS: 50})
	if err == nil {
		t.Fatalf("a busy database returned no error (integrity %q, findings %+v)", rep.Integrity, rep.Findings)
	}
	if rep.Integrity != "unchecked" {
		t.Fatalf("integrity = %q for a busy database, want unchecked", rep.Integrity)
	}
	if got := find(rep, KindIntegrity); len(got) != 0 {
		t.Fatalf("a busy database was reported as damaged: %+v", got)
	}
}
