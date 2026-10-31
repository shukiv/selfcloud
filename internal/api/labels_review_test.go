// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// hasValue reports whether a node's labels, as the caller sees them, hold a value for the field.
func hasValue(labels map[string]nodeLabelJSON, fieldID string) bool {
	it, ok := labels[fieldID]
	return ok && it.Value != nil
}

// refusedRestricted wants a 403 whose reason is restricted_label.
func refusedRestricted(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	e := wantError(t, rec, 403, apierr.Forbidden)
	if e.Details["reason"] != "restricted_label" {
		t.Errorf("reason %v, want restricted_label", e.Details["reason"])
	}
}

// TestMoveOfRestrictedValueNeedsAManager: a move never raises who can read a restricted value, unless a manager
// of the item makes it. A non-manager may not take an item that holds one out of its folder, or into a place
// where it would be a manager. Moves that keep the item in its folder with no higher role, and moves without
// any restricted value, stay open to an editor. The key, which manages everything, moves freely.
func TestMoveOfRestrictedValueNeedsAManager(t *testing.T) {
	t.Parallel()
	t.Run("an editor cannot take a file into My files", func(t *testing.T) {
		t.Parallel()
		lf := newLabelFixture(t)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
		pf := lf.personalFolder(t, lf.annSess)
		refusedRestricted(t, lf.annDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/move", map[string]any{"folder": pf}))
		if hasValue(nodeLabels(t, lf.annDo, lf.deep.ID), lf.lead) {
			t.Error("the refused move still let the editor read the restricted value")
		}
		if !hasValue(nodeLabels(t, lf.keyDo, lf.deep.ID), lf.lead) {
			t.Error("the refused move cleared the restricted value")
		}
	})
	t.Run("an editor cannot take a folder that holds one out of its folder", func(t *testing.T) {
		t.Parallel()
		lf := newLabelFixture(t)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
		pf := lf.personalFolder(t, lf.annSess)
		refusedRestricted(t, lf.annDo("POST", "/api/v1/nodes/"+lf.b.ID+"/move", map[string]any{"folder": pf}))
	})
	t.Run("an editor cannot move into a place where they manage", func(t *testing.T) {
		t.Parallel()
		lf := newLabelFixture(t)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
		lf.setPerm(t, lf.c.ID, lf.ann.ID, "manager")
		refusedRestricted(t, lf.annDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/move", map[string]any{"parent": lf.c.ID}))
	})
	t.Run("an editor moves a restricted item inside its folder with no higher role", func(t *testing.T) {
		t.Parallel()
		lf := newLabelFixture(t)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
		lf.setPerm(t, lf.a.ID, lf.ann.ID, "editor")
		wantStatus(t, lf.annDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/move", map[string]any{"parent": lf.c.ID}), 200)
		if hasValue(nodeLabels(t, lf.annDo, lf.deep.ID), lf.lead) {
			t.Error("the editor reads the restricted value after an inside move")
		}
	})
	t.Run("an editor moves an item with no restricted value anywhere", func(t *testing.T) {
		t.Parallel()
		lf := newLabelFixture(t)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.note, "plain", 204)
		lf.setPerm(t, lf.a.ID, lf.ann.ID, "editor")
		pf := lf.personalFolder(t, lf.annSess)
		wantStatus(t, lf.annDo("POST", "/api/v1/nodes/"+lf.sib.ID+"/move", map[string]any{"folder": pf}), 200)
	})
	t.Run("the key moves a restricted item freely and keeps the value", func(t *testing.T) {
		t.Parallel()
		lf := newLabelFixture(t)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
		wantStatus(t, lf.keyDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/move", map[string]any{"parent": lf.c.ID}), 200)
		if !hasValue(nodeLabels(t, lf.keyDo, lf.deep.ID), lf.lead) {
			t.Error("a manager's move lost the restricted value")
		}
	})
}

// copyJob queues a copy as the caller and returns the job, with every copy made as a job.
func copyJob(t *testing.T, s *Server, do asCaller, nodeID string, body map[string]any) (*store.Job, copyPayload) {
	t.Helper()
	s.copySyncNodes = 0
	rec := do("POST", "/api/v1/nodes/"+nodeID+"/copy", body)
	wantStatus(t, rec, 202)
	job, err := s.store.Job(context.Background(), path.Base(rec.Header().Get("Location")))
	if err != nil {
		t.Fatal(err)
	}
	var pl copyPayload
	if err := json.Unmarshal([]byte(job.Payload), &pl); err != nil {
		t.Fatal(err)
	}
	return job, pl
}

// TestCopyOfRestrictedValueNeedsAManager: a copy carries a restricted value only when the caller manages the
// source. A copy by anyone else keeps the other values and drops the restricted one, without an error. The
// queued copy, which runs later as its caller, follows the same rule.
func TestCopyOfRestrictedValueNeedsAManager(t *testing.T) {
	t.Parallel()
	// seeded makes a fresh fixture whose source has a note and a restricted value.
	seeded := func(t *testing.T) *labelFixture {
		lf := newLabelFixture(t)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.note, "kept", 204)
		setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
		return lf
	}
	// checkCopy reads the copy as the key: the unrestricted value is there, and the restricted one only if wantLead.
	checkCopy := func(t *testing.T, lf *labelFixture, copyID string, wantLead bool) {
		t.Helper()
		got := nodeLabels(t, lf.keyDo, copyID)
		if got[lf.note].Value != "kept" {
			t.Errorf("copy lost its unrestricted value: %+v", got[lf.note])
		}
		if hasValue(got, lf.lead) != wantLead {
			t.Errorf("copy holds restricted value = %v, want %v", hasValue(got, lf.lead), wantLead)
		}
	}

	t.Run("sync", func(t *testing.T) {
		t.Parallel()
		lf := seeded(t)
		pfBob := lf.personalFolder(t, lf.bobSess)
		rec := lf.bobDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/copy", map[string]any{"folder": pfBob})
		wantStatus(t, rec, 201)
		checkCopy(t, lf, decode[nodeJSON](t, rec).ID, false)

		pfAnn := lf.personalFolder(t, lf.annSess)
		rec = lf.annDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/copy", map[string]any{"folder": pfAnn})
		wantStatus(t, rec, 201)
		checkCopy(t, lf, decode[nodeJSON](t, rec).ID, false)

		rec = lf.keyDo("POST", "/api/v1/nodes/"+lf.deep.ID+"/copy", map[string]any{"parent": lf.c.ID})
		wantStatus(t, rec, 201)
		checkCopy(t, lf, decode[nodeJSON](t, rec).ID, true)
	})

	t.Run("queued", func(t *testing.T) {
		t.Parallel()
		lf := seeded(t)
		pfAnn := lf.personalFolder(t, lf.annSess)
		job, pl := copyJob(t, lf.s, lf.annDo, lf.deep.ID, map[string]any{"folder": pfAnn})
		if err := lf.s.runCopyJob(context.Background(), job); err != nil {
			t.Fatalf("copy job by the editor = %v", err)
		}
		checkCopy(t, lf, pl.Root, false)

		job, pl = copyJob(t, lf.s, lf.keyDo, lf.deep.ID, map[string]any{"parent": lf.c.ID})
		if err := lf.s.runCopyJob(context.Background(), job); err != nil {
			t.Fatalf("copy job by the key = %v", err)
		}
		checkCopy(t, lf, pl.Root, true)
	})
}

// TestRestrictedOffRecordsTheValueCount: a field that stops being restricted shows its values to every reader at
// once, so the audit entry names how many values that was.
func TestRestrictedOffRecordsTheValueCount(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
	wantStatus(t, lf.keyDo("PATCH", "/api/v1/accounts/"+lf.acc.ID+"/label-fields/"+lf.lead,
		map[string]any{"restricted": false}), 200)
	for _, e := range auditActionsOf(t, lf.s, lf.acc.ID, "label_field.update") {
		if e.Target != lf.lead {
			continue
		}
		if fmt.Sprint(e.Details["values"]) != "1" {
			t.Errorf("audit values = %v, want 1 (details %v)", e.Details["values"], e.Details)
		}
		return
	}
	t.Fatal("no label_field.update entry for the field")
}

// TestDeleteUserAuditsTheValuesThatGo: removing a user removes the values that name it, and the audit entry of the
// removal says how many.
func TestDeleteUserAuditsTheValuesThatGo(t *testing.T) {
	t.Parallel()
	lf := newLabelFixture(t)
	setLabel(t, lf.keyDo, lf.deep.ID, lf.lead, lf.bob.ID, 204)
	wantStatus(t, lf.keyDo("DELETE", "/api/v1/accounts/"+lf.acc.ID+"/users/"+lf.bob.ID, nil), 204)
	for _, e := range auditActionsOf(t, lf.s, lf.acc.ID, "user.delete") {
		if e.Target != lf.bob.ID {
			continue
		}
		if fmt.Sprint(e.Details["labels"]) != "1" {
			t.Errorf("audit labels = %v, want 1 (details %v)", e.Details["labels"], e.Details)
		}
		return
	}
	t.Fatal("no user.delete entry for the user")
}
