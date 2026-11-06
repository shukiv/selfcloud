// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// tarGzFiles builds a gzipped tar of small regular files, one per name, each holding its name.
func tarGzFiles(t *testing.T, names []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(n))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// turnClock is the clock of one turn in a test: its first reading is the turn's start, its second is the time the walk
// reaches the first new entry, and every later reading is later.
func turnClock(start, reached, later time.Time) func() time.Time {
	n := 0
	return func() time.Time {
		n++
		switch n {
		case 1:
			return start
		case 2:
			return reached
		}
		return later
	}
}

// importItemCount counts the items an import has written, one per entry it staged.
func importItemCount(t *testing.T, fx *importFixture, impID string) int {
	t.Helper()
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM import_items WHERE import_id = ?`, impID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestTurnAfterASlowReachStagesAtLeastOneEntry: the walk takes 3 s to reach the first new entry, and the window runs out
// 20 s into the turn, before the second entry. The turn still stages its first entry, since each turn makes progress
// (round 9; mutation: the rule removed).
func TestTurnAfterASlowReachStagesAtLeastOneEntry(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importYield = time.Nanosecond
	T := time.Now()
	fx.s.importNow = turnClock(T, T.Add(3*time.Second), T.Add(20*time.Second))
	imp := fx.startOK(t, fx.archive(t, "five.tar.gz", tarGzFiles(t, []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"})), "")
	_ = fx.runJob(t)
	if n := importItemCount(t, fx, imp.ID); n < 1 {
		t.Fatalf("the turn staged %d entries, want at least 1", n)
	}
}

// TestTurnAfterASlowReachStagesMoreThanOneEntry: the walk takes 3 s to reach the first new entry, and the window of
// 2 × 3 s runs out 9 s into the turn; the entries before then are staged in that turn. Before the fix the window ran from
// the turn's start, so the turn yielded after its first entry (round 9; mutation: the window started at the turn's start).
func TestTurnAfterASlowReachStagesMoreThanOneEntry(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importYield = time.Nanosecond
	T := time.Now()
	fx.s.importNow = turnClock(T, T.Add(3*time.Second), T.Add(7*time.Second))
	imp := fx.startOK(t, fx.archive(t, "five.tar.gz", tarGzFiles(t, []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"})), "")
	_ = fx.runJob(t)
	if n := importItemCount(t, fx, imp.ID); n < 2 {
		t.Fatalf("the turn after a 3 s reach staged %d entries, want more than one", n)
	}
}

// TestImportWindowIsTheBaseOrTwiceTheReach: a turn's window is at least its base, and twice the time the walk took to reach
// its first new entry, so the re-read of an archive costs at most about a third of a turn (round 9).
func TestImportWindowIsTheBaseOrTwiceTheReach(t *testing.T) {
	const base = 30 * time.Second
	cases := []struct {
		name        string
		reach, want time.Duration
	}{
		{"no reach gives the base", 0, base},
		{"a reach of 10 s under a base of 30 s gives the base", 10 * time.Second, base},
		{"a reach of 40 s gives twice the reach", 40 * time.Second, 80 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := importWindow(c.reach, base); got != c.want {
				t.Fatalf("importWindow(%v, %v) = %v, want %v", c.reach, base, got, c.want)
			}
		})
	}
}

// TestGzipTarStagesAtLeastOneEntryPerTurn: with a yield after every turn's first moment, a gzipped tar of 20 entries still
// finishes. Each turn stages at least one entry, so the import is done in at most 20 turns and one publish turn, and no turn
// yields with no progress (round 9). Before the fix, every turn yielded before its first new entry, so the import never
// moved, since a gzip stream is read from its start to reach any entry.
func TestGzipTarStagesAtLeastOneEntryPerTurn(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importYield = time.Nanosecond
	const n = 20
	var names []string
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("f%02d.txt", i))
	}
	imp := fx.startOK(t, fx.archive(t, "many.tar.gz", tarGzFiles(t, names)), "")
	turns := 0
	for ; turns < n+2; turns++ {
		err := fx.runJob(t)
		if err == nil {
			break
		}
		if !errors.Is(err, jobs.ErrYield) {
			t.Fatalf("turn %d failed: %v", turns+1, err)
		}
	}
	if got := fx.get(t, imp.ID); got.State != store.ImportDone {
		t.Fatalf("after %d turns the import is %s, want done within %d turns: a turn yields with no progress", turns, got.State, n+2)
	}
}
