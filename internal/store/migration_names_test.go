// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenRefusesAppliedNameMismatch: a database whose version 35 was recorded under another file name is refused, and
// the error names the version and both files. Nothing is applied first. Without the check, this database would open, since
// its version is already current, and the other build's 0035 would never run on it.
func TestOpenRefusesAppliedNameMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selfcloud.db")
	seedMigratedDB(t, path)
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, "UPDATE schema_migrations SET name = '0035_search_content.sql' WHERE version = 35"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, Options{Path: path})
	if err == nil {
		s.Close()
		t.Fatal("Open accepted a database whose version 35 has another name here")
	}
	for _, want := range []string{"migration 35", "0035_search_content.sql", "0035_children_sort.sql"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestOpenAcceptsMatchingNames: the same database with its recorded names left alone opens, so the check is not a refusal
// of every reopen.
func TestOpenAcceptsMatchingNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selfcloud.db")
	seedMigratedDB(t, path)
	s, err := Open(context.Background(), Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
