// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
)

// TestMigration0044KeepsRowsAndAcceptsCommentEvents runs the real runner (applyMigrations) up to migration 43 on an empty
// database: migrate has no stop-at-version option, so the test calls the function migrate calls, with the schema_migrations
// table migrate creates. It then seeds an endpoint with its subscriptions, a queued event and its delivery, and applies 44
// alone. The rows must all survive, the delivery must still point at its event, the outbox's ID counter must carry over,
// PRAGMA foreign_key_check must be clean, and the comment events must be accepted, while an unknown name is still refused.
func TestMigration0044KeepsRowsAndAcceptsCommentEvents(t *testing.T) {
	ctx := context.Background()
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) < 44 || migs[43].version != 44 {
		t.Fatalf("migration 44 is not in the embedded list (%d migrations)", len(migs))
	}

	path := filepath.Join(t.TempDir(), "at43.db")
	w, err := sql.Open("sqlite", dsn(path, []string{"busy_timeout(10000)", "foreign_keys(1)", "synchronous(NORMAL)", "journal_mode(WAL)"}, "immediate"))
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	t.Cleanup(func() { w.Close() })
	s := &Store{w: w, log: slog.New(slog.DiscardHandler)}

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := w.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := w.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}

	// The schema_migrations table as migrate creates it, then the runner's own loop up to 43.
	exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	)`)
	if err := s.applyMigrations(ctx, migs[:43]); err != nil {
		t.Fatal(err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 43 {
		t.Fatalf("schema version = %d (%v), want 43", v, err)
	}

	// Seed at 43: an account, an endpoint with two subscriptions, one queued event, its delivery, and one event row
	// written and then deleted at the top of the table, so the counter is ahead of the largest row.
	exec(`INSERT INTO accounts (id, name, status, created_at, updated_at) VALUES ('acc_1', 'Acme', 'active', 0, 0)`)
	exec(`INSERT INTO webhook_endpoints (id, account_id, url, state, secret_enc, created_by, created_at, updated_at)
		VALUES ('whk_1', 'acc_1', 'https://hooks.example.com/in', 'active', X'0102', 'key_test', 0, 0)`)
	exec(`INSERT INTO webhook_endpoint_events (endpoint_id, account_id, event) VALUES ('whk_1', 'acc_1', 'file.created')`)
	exec(`INSERT INTO webhook_endpoint_events (endpoint_id, account_id, event) VALUES ('whk_1', 'acc_1', 'quota.warning')`)
	exec(`INSERT INTO webhook_events (event_id, account_id, type, payload, created_at) VALUES ('evt_1', 'acc_1', 'file.created', '{}', 0)`)
	exec(`INSERT INTO webhook_events (event_id, account_id, type, payload, created_at) VALUES ('evt_top', 'acc_1', 'file.created', '{}', 0)`)
	exec(`DELETE FROM webhook_events WHERE event_id = 'evt_top'`)
	exec(`INSERT INTO webhook_deliveries (id, account_id, endpoint_id, event_row_id, state, attempts, next_attempt_at, created_at)
		VALUES ('dlv_1', 'acc_1', 'whk_1', (SELECT id FROM webhook_events WHERE event_id = 'evt_1'), 'pending', 0, 0, 0)`)

	// At 43 the comment names are not in the list, so a row with one is refused.
	if _, err := w.ExecContext(ctx, `INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
		VALUES ('evt_before', 'acc_1', 'comment.created', '{}', 0)`); err == nil {
		t.Fatal("comment.created was accepted before migration 44")
	}

	tables := []string{"accounts", "webhook_endpoints", "webhook_endpoint_events", "webhook_events", "webhook_deliveries"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = count("SELECT COUNT(*) FROM " + tb)
	}
	seqBefore := count("SELECT seq FROM sqlite_sequence WHERE name = 'webhook_events'")
	if seqBefore != 2 {
		t.Fatalf("sqlite_sequence before 44 = %d, want 2", seqBefore)
	}

	// Migration 44 alone, through the runner.
	if err := s.applyMigrations(ctx, migs[43:44]); err != nil {
		t.Fatal(err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 44 {
		t.Fatalf("schema version = %d (%v), want 44", v, err)
	}

	for _, tb := range tables {
		if got := count("SELECT COUNT(*) FROM " + tb); got != before[tb] {
			t.Errorf("%s rows after 44 = %d, want %d", tb, got, before[tb])
		}
	}
	var eventOfDelivery string
	if err := w.QueryRowContext(ctx, `SELECT e.event_id FROM webhook_deliveries d
		JOIN webhook_events e ON e.id = d.event_row_id WHERE d.id = 'dlv_1' AND d.endpoint_id = 'whk_1'`).Scan(&eventOfDelivery); err != nil {
		t.Fatalf("the delivery no longer points at its event: %v", err)
	}
	if eventOfDelivery != "evt_1" {
		t.Fatalf("the delivery points at %s, want evt_1", eventOfDelivery)
	}
	if got := count("SELECT seq FROM sqlite_sequence WHERE name = 'webhook_events'"); got != seqBefore {
		t.Fatalf("sqlite_sequence after 44 = %d, want %d carried over", got, seqBefore)
	}
	var violations int
	rows, err := w.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("PRAGMA foreign_key_check reports %d violations", violations)
	}

	// The comment names are accepted now, in the outbox and in the subscriptions. A new event takes the next ID, not
	// the ID of the deleted row.
	comments := []string{"comment.created", "comment.updated", "comment.deleted", "comment.resolved", "comment.reopened"}
	for i, name := range comments {
		exec(`INSERT INTO webhook_events (event_id, account_id, type, payload, created_at) VALUES (?, 'acc_1', ?, '{}', 0)`,
			"evt_"+name, name)
		if i == 0 {
			if got := count("SELECT id FROM webhook_events WHERE event_id = ?", "evt_"+name); got != seqBefore+1 {
				t.Fatalf("new comment event got id %d, want %d", got, seqBefore+1)
			}
		}
	}
	for _, name := range comments {
		exec(`INSERT INTO webhook_endpoint_events (endpoint_id, account_id, event) VALUES ('whk_1', 'acc_1', ?)`, name)
	}
	if _, err := w.ExecContext(ctx, `INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
		VALUES ('evt_bogus', 'acc_1', 'comment.archived', '{}', 0)`); err == nil {
		t.Fatal("an unknown event name was accepted after migration 44")
	}
}
