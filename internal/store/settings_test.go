// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"maps"
	"testing"
)

// TestSettingsRows: a row is added, replaced and removed, and the table is read whole.
func TestSettingsRows(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if rows, err := s.Settings(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("a new table = %v, %v; want empty", rows, err)
	}
	write := func(f func(tx *Tx) error) {
		t.Helper()
		if err := s.Write(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	write(func(tx *Tx) error {
		if err := tx.SetSetting(ctx, "trash.retention", "48h0m0s"); err != nil {
			return err
		}
		return tx.SetSetting(ctx, "search.enabled", "false")
	})
	write(func(tx *Tx) error { return tx.SetSetting(ctx, "trash.retention", "72h0m0s") })
	rows, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"trash.retention": "72h0m0s", "search.enabled": "false"}; !maps.Equal(rows, want) {
		t.Errorf("rows = %v, want %v", rows, want)
	}
	write(func(tx *Tx) error {
		if err := tx.DeleteSetting(ctx, "search.enabled"); err != nil {
			return err
		}
		// Removing a row that is not there is not an error: a reset of a key with no row has nothing to remove.
		return tx.DeleteSetting(ctx, "versions.max_age")
	})
	rows, err = s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"trash.retention": "72h0m0s"}; !maps.Equal(rows, want) {
		t.Errorf("rows after delete = %v, want %v", rows, want)
	}
	// A key with SQL in it is a value like any other: the statements bind it.
	odd := "x'); DROP TABLE settings; --"
	write(func(tx *Tx) error { return tx.SetSetting(ctx, odd, "1") })
	if rows, err := s.Settings(ctx); err != nil || rows[odd] != "1" || len(rows) != 2 {
		t.Errorf("rows with an odd key = %v, %v", rows, err)
	}
}
