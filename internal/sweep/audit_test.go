// SPDX-License-Identifier: AGPL-3.0-or-later

package sweep

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/blob"
	"github.com/shukiv/selfcloud/internal/id"
	"github.com/shukiv/selfcloud/internal/store"
	_ "modernc.org/sqlite"
)

// openAuditStore opens a store and an account to hold the entries the audit tests write.
func openAuditStore(t *testing.T) (*store.Store, *blob.Dir, string, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "selfcloud.db")
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	blobs, err := blob.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc := &store.Account{ID: id.New("acc"), Name: "A", Status: store.AccountActive, CreatedAt: store.Now(), UpdatedAt: store.Now()}
	if err := st.Write(ctx, func(tx *store.Tx) error { return tx.InsertAccount(ctx, acc) }); err != nil {
		t.Fatal(err)
	}
	return st, blobs, acc.ID, path
}

// auditAt writes n entries at the given time, for the account or for the installation when account is "".
func auditAt(t *testing.T, st *store.Store, account string, at time.Time, n int, action string) {
	t.Helper()
	if err := st.Write(context.Background(), func(tx *store.Tx) error {
		for i := 0; i < n; i++ {
			if err := tx.InsertAudit(context.Background(), &store.AuditEntry{Time: at, AccountID: account,
				ActorKind: "system", ActorID: "test", Action: action}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// countAudit counts the entries with the given action, read through a second connection.
func countAudit(t *testing.T, path, action string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM audit_log WHERE action = ?", action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAuditRetentionRemovesOnlyPastTheLimit: with a 30-day limit, an entry 40 days old goes, one 29 days 23 hours old
// stays, and an installation-wide entry 40 days old goes too.
func TestAuditRetentionRemovesOnlyPastTheLimit(t *testing.T) {
	ctx := context.Background()
	st, blobs, acc, path := openAuditStore(t)
	now := time.Unix(1_700_000_000, 0)
	day := 24 * time.Hour
	auditAt(t, st, acc, now.Add(-40*day), 1, "old.account")
	auditAt(t, st, acc, now.Add(-29*day-23*time.Hour), 1, "inside.account")
	auditAt(t, st, "", now.Add(-40*day), 1, "old.installation")
	auditAt(t, st, "", now.Add(-29*day-23*time.Hour), 1, "inside.installation")
	// An entry exactly at the limit is kept: only entries older than it go.
	auditAt(t, st, acc, now.Add(-30*day), 1, "at.limit")

	var logs bytes.Buffer
	sw := New(Options{Store: st, Blobs: blobs, AuditRetention: 30 * day,
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.AuditPruned != 2 {
		t.Errorf("pruned %d entries, want 2", res.AuditPruned)
	}
	for action, want := range map[string]int{"old.account": 0, "old.installation": 0, "inside.account": 1, "inside.installation": 1, "at.limit": 1} {
		if got := countAudit(t, path, action); got != want {
			t.Errorf("%s: %d entries left, want %d", action, got, want)
		}
	}
	if !strings.Contains(logs.String(), "audit log pruned") || !strings.Contains(logs.String(), "removed=2") {
		t.Errorf("the count was not logged at info; log:\n%s", logs.String())
	}
}

// TestAuditRetentionZeroKeepsEverything: 0 keeps every entry, however old.
func TestAuditRetentionZeroKeepsEverything(t *testing.T) {
	ctx := context.Background()
	st, blobs, acc, path := openAuditStore(t)
	now := time.Unix(1_700_000_000, 0)
	auditAt(t, st, acc, now.Add(-10*365*24*time.Hour), 3, "ancient")
	sw := New(Options{Store: st, Blobs: blobs})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.AuditPruned != 0 || countAudit(t, path, "ancient") != 3 {
		t.Errorf("pruned %d with retention 0; %d entries left", res.AuditPruned, countAudit(t, path, "ancient"))
	}
}

// TestAuditRetentionPassesAreBoundedAndHourly: a backlog of 2,500 entries is removed by one pass, in batches of 1,000.
// A pass within the hour after one that ran does nothing, so the work is spread over hours and not held in one sweep.
func TestAuditRetentionPassesAreBoundedAndHourly(t *testing.T) {
	ctx := context.Background()
	st, blobs, acc, path := openAuditStore(t)
	now := time.Unix(1_700_000_000, 0)
	auditAt(t, st, acc, now.Add(-100*24*time.Hour), 2500, "backlog")
	sw := New(Options{Store: st, Blobs: blobs, AuditRetention: 30 * 24 * time.Hour})
	res, err := sw.Once(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.AuditPruned != 2500 {
		t.Errorf("first pass pruned %d, want 2500 in batches of 1000", res.AuditPruned)
	}
	auditAt(t, st, acc, now.Add(-100*24*time.Hour), 5, "later")
	res, err = sw.Once(ctx, now.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res.AuditPruned != 0 || countAudit(t, path, "later") != 5 {
		t.Errorf("a pass ten minutes later pruned %d; want none", res.AuditPruned)
	}
	res, err = sw.Once(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.AuditPruned != 5 {
		t.Errorf("a pass an hour later pruned %d, want 5", res.AuditPruned)
	}
}

// TestAuditRetentionErrorDoesNotStopTheSweep: when the audit log cannot be read, the pass counts the failure and the
// rest of the sweep still runs, so Once returns no error.
func TestAuditRetentionErrorDoesNotStopTheSweep(t *testing.T) {
	ctx := context.Background()
	st, blobs, _, path := openAuditStore(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE audit_log"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var logs bytes.Buffer
	sw := New(Options{Store: st, Blobs: blobs, AuditRetention: time.Hour,
		Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	res, err := sw.Once(ctx, store.Now())
	if err != nil {
		t.Fatalf("Once stopped on the audit error: %v", err)
	}
	if res.Failed == 0 || !strings.Contains(logs.String(), "audit retention") {
		t.Errorf("the failure was not counted or logged: failed=%d, log:\n%s", res.Failed, logs.String())
	}
}
