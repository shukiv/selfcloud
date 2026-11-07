// SPDX-License-Identifier: AGPL-3.0-or-later

package fsck

import (
	"testing"
	"time"
)

// A run that stops before the integrity check has not checked anything, so its report must not say "ok".
// Mutant: start Integrity as "ok" again; the refused run's report then says it passed.
func TestRunThatStopsBeforeTheCheckIsNotOK(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rep, err := Run(f.ctx, Options{DataDir: f.dir, DBPath: f.dbPath, MinAge: -time.Second})
	if err == nil {
		t.Fatal("a negative minimum age was accepted")
	}
	if rep.Integrity != "unchecked" {
		t.Fatalf("integrity = %q for a run that never checked, want unchecked", rep.Integrity)
	}
}
