// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
)

// migrationVersion returns the embedded migration with the given version, read through the same loader Open uses.
func migrationVersion(t *testing.T, version int) migration {
	t.Helper()
	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if m.version == version {
			return m
		}
	}
	t.Fatalf("no migration %d", version)
	return migration{}
}

// The rebuild of webhook_events in migration 0021 keeps the ID counter. Events whose rows were deleted at the top of
// the table (the highest IDs) must not have their IDs handed out again. The sweep deletes events that no delivery
// refers to, so this can happen after the table is rebuilt.
func TestMigration0021KeepsEventCounter(t *testing.T) {
	ctx := context.Background()
	s := openAt18(t, seedAt18(t))
	var mig []migration
	for _, v := range []int{19, 20} {
		mig = append(mig, migrationVersion(t, v))
	}
	if err := s.applyMigrations(ctx, mig); err != nil {
		t.Fatal(err)
	}
	var acc string
	if err := s.w.QueryRowContext(ctx, `SELECT id FROM accounts LIMIT 1`).Scan(&acc); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"evt_one", "evt_two", "evt_top"} {
		if _, err := s.w.ExecContext(ctx, `INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
			VALUES (?, ?, 'file.created', '{}', 0)`, ev, acc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.ExecContext(ctx, `DELETE FROM webhook_events WHERE event_id = 'evt_top'`); err != nil {
		t.Fatal(err)
	}
	if err := s.applyMigrations(ctx, []migration{migrationVersion(t, 21)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO webhook_events (event_id, account_id, type, payload, created_at)
		VALUES ('evt_after', ?, 'file.created', '{}', 0)`, acc); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := s.w.QueryRowContext(ctx, `SELECT id FROM webhook_events WHERE event_id = 'evt_after'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after <= 3 {
		t.Fatalf("new event got id %d, but id 3 was deleted: the counter must carry over, so want id 4 or more", after)
	}
}
