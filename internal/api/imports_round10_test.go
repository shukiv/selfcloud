// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/jobs"
	"github.com/shukiv/selfcloud/internal/store"
)

// A stage turn whose context is cancelled returns the context's error and leaves the import where it was: running, in
// the stage phase, with its cursor unchanged. A shutdown does not fail the import. The cancellation comes at the
// stage hook, before the first store read of the turn, so the cursor cannot move.
func TestCancelledStageTurnLeavesTheImportInStage(t *testing.T) {
	fx := newImportFixture(t)
	fx.s.importYield = time.Nanosecond
	var names []string
	for i := 0; i < 20; i++ {
		names = append(names, fmt.Sprintf("f%02d.txt", i))
	}
	imp := fx.startOK(t, fx.archive(t, "many.tar.gz", tarGzFiles(t, names)), "")
	if err := fx.runJob(t); !errors.Is(err, jobs.ErrYield) {
		t.Fatalf("the first turn: %v, want a yield after its first entries", err)
	}
	ctx := context.Background()
	before, err := fx.s.store.Import(ctx, imp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Phase != store.ImportPhaseStage || before.Cursor < 0 {
		t.Fatalf("after the first turn the import is in phase %s at cursor %d, want stage", before.Phase, before.Cursor)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	fx.s.importFault = func(stage string) error {
		if stage == "stage" {
			cancel()
		}
		return nil
	}
	job, err := fx.s.store.ClaimJobOf(ctx, jobImport)
	if err != nil {
		t.Fatalf("no import job is queued: %v", err)
	}
	runErr := fx.s.runImportJob(cancelCtx, job)
	fx.s.importFault = nil
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("the cancelled turn returned %v, want context.Canceled", runErr)
	}
	after, err := fx.s.store.Import(ctx, imp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != store.ImportRunning || after.Phase != store.ImportPhaseStage || after.Cursor != before.Cursor {
		t.Fatalf("after the cancelled turn the import is %s in phase %s at cursor %d, want running in stage at cursor %d",
			after.State, after.Phase, after.Cursor, before.Cursor)
	}
}
