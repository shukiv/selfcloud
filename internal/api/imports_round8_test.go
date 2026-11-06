// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// A directory the import reuses, purged while the import is staged, is a target that moved: the publish fails with conflict,
// as for any other moved directory, not as an internal error (round 8, R7-4). The purge is of the reused directory, not of
// the import's target, so EndImportsAt does not end the import: the publish finds the directory gone.
func TestPurgedReusedDirectoryFailsWithTargetMoved(t *testing.T) {
	fx := newImportFixture(t)
	data := zipArchive(t, [][2]string{{"t/a.txt", "one"}})
	target := mkdirIn(t, fx, fx.folder, "", "D")
	_ = importInto(t, fx, target.ID, data)
	reused := childrenOfNode(t, fx, target.ID)["t"]
	// An archive's file the import already brought in is skipped, so it is removed first, as an entry that is gone is
	// imported again under the reused directory.
	a := childrenOfNode(t, fx, reused.ID)["a.txt"]
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+a.ID, fx.key, nil), http.StatusNoContent)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+a.ID, fx.key, nil), http.StatusNoContent)
	// The second import stages its entries under a placeholder of t, and yields at its publish.
	imp := stagedFiles(t, fx, fx.folder, target.ID, data, "")
	// The precondition: the second import reused t, so its publish checks t.
	db, err := sql.Open("sqlite", fx.s.cfg.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	var reusing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM import_items WHERE import_id = ? AND reuses = ?`, imp.ID, reused.ID).Scan(&reusing); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if reusing != 1 {
		t.Fatalf("the second import reuses %d items of t, want 1", reusing)
	}
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/nodes/"+reused.ID, fx.key, nil), http.StatusNoContent)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/trash/"+reused.ID, fx.key, nil), http.StatusNoContent)
	_ = fx.runJob(t)
	got := fx.get(t, imp.ID)
	if got.State != store.ImportFailed || got.ErrorCode != string(apierr.Conflict) {
		t.Fatalf("the publish after the purge: state %s code %q, want failed with conflict", got.State, got.ErrorCode)
	}
}

// A poison import ends with the same owner-visible record as any failed import: one import.finish audit entry and one
// import.failed event, written in the write that cleans it up (round 8, R7-3). Before, it ended with neither.
func TestPoisonImportEndsWithAFailureEventAndAudit(t *testing.T) {
	ctx := context.Background()
	wf := newWebhookFixture(t)
	wf.create(t, map[string]any{"url": "https://hooks.example.com/in", "events": []string{"import.failed"}})
	u, sess := verifiedUser(t, wf.s, wf.key, wf.acc.ID, "ann@example.com")
	pf, err := wf.s.store.PersonalFolder(ctx, wf.acc.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	fx := &importFixture{s: wf.s, key: wf.key, acc: wf.acc, user: u, sess: sess, folder: pf.ID}
	// The stage panics: the job dies without importFail, so the import stays running and no failure is written by the run.
	// Only the poison end writes it.
	fx.s.importFault = func(stage string) error {
		if stage == "stage" {
			panic("the stage broke")
		}
		return nil
	}
	_ = fx.startOK(t, fx.archive(t, "one.zip", zipArchive(t, [][2]string{{"a.txt", "1"}})), "")
	_ = fx.runJob(t)
	if n := failedEvents(t, fx); n != 0 {
		t.Fatalf("%d import.failed events before the poison end, want none", n)
	}
	for i := 0; i < 6; i++ {
		n, err := fx.s.requeueImports(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		_ = fx.runJob(t)
	}
	fx.s.importFault = nil
	failed := failedEvents(t, fx)
	if failed != 1 {
		t.Fatalf("%d import.failed events for the poison import, want 1", failed)
	}
	finished := 0
	for _, a := range fx.auditActions(t) {
		if a == "import.finish" {
			finished++
		}
	}
	if finished != 1 {
		t.Fatalf("%d import.finish audit entries for the poison import, want 1", finished)
	}
	// A second sweep finds nothing to end: the import was ended once.
	if n, err := fx.s.requeueImports(ctx); err != nil || n != 0 {
		t.Fatalf("a second sweep queued %d jobs (err %v), want none", n, err)
	}
	if n := failedEvents(t, fx); n != 1 {
		t.Fatalf("the second sweep left %d import.failed events, want 1", n)
	}
}

// failedEvents counts the import.failed events of the account.
func failedEvents(t *testing.T, fx *importFixture) int {
	t.Helper()
	n := 0
	for _, ev := range outboxOf(t, fx.s, fx.acc.ID) {
		if ev.Type == "import.failed" {
			n++
		}
	}
	return n
}
