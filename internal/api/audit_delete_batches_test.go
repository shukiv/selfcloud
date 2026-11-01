// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
	_ "modernc.org/sqlite"
)

// TestAccountDeleteInBatchesAndAfterACrash: an account with more audit entries than one batch is deleted. The job first
// removes the entries in batches; a crash after one batch leaves the rest to a rerun, which finishes the deletion and
// names the account in the installation-wide record.
func TestAccountDeleteInBatchesAndAfterACrash(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	admin := adminKey(t, s)
	big, _ := newAccount(t, s, admin, "Big Ltd")
	keep, _ := newAccount(t, s, admin, "Keep Ltd")
	if err := s.store.Write(ctx, func(tx *store.Tx) error {
		for i := 0; i < 2500; i++ {
			if err := tx.InsertAudit(ctx, &store.AuditEntry{Time: store.Now(), AccountID: big.ID,
				ActorKind: "system", ActorID: "seed", Action: "seed"}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec := call(t, s, "DELETE", "/api/v1/accounts/"+big.ID, admin, nil)
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)

	// The crash: one batch was removed, and the job never got further.
	if n, err := s.store.PruneAccountAudit(ctx, big.ID, accountAuditBatch); err != nil || n != accountAuditBatch {
		t.Fatalf("first batch removed %d, %v", n, err)
	}
	if err := s.jobs.RunPending(ctx); err != nil {
		t.Fatal(err)
	}
	if got := decode[jobJSON](t, call(t, s, "GET", "/api/v1/jobs/"+job.ID, admin, nil)); got.Status != "succeeded" {
		t.Fatalf("job after the rerun = %+v", got)
	}

	db, err := sql.Open("sqlite", s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("SELECT COUNT(*) FROM audit_log WHERE account_id = ?", big.ID); n != 0 {
		t.Errorf("the deleted account still has %d entries", n)
	}
	if n := count("SELECT COUNT(*) FROM audit_log WHERE account_id = ?", keep.ID); n == 0 {
		t.Errorf("another account lost its entries")
	}
	if n := count("SELECT COUNT(*) FROM audit_log WHERE action = 'account.deleted' AND account_id IS NULL AND target = 'Big Ltd'"); n != 1 {
		t.Errorf("installation-wide account.deleted naming Big Ltd = %d rows, want 1", n)
	}
}
