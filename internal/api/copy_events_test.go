// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"testing"
)

// copyEventCount copies the tree (with a shortcut in it) as one write or as a chunked job, and counts the file.created
// events the copy writes. The tree is the same in both: five files and a shortcut to one of them.
func copyEventCount(t *testing.T, chunked bool) int {
	t.Helper()
	fx := newWebhookFixture(t)
	fx.create(t, nil) // an endpoint that takes file.created
	s := fx.s
	f := newFolder(t, s, fx.key, fx.acc.ID, "Docs")
	tree := bigTree(t, s, fx.key, f.ID)
	// The shortcut sits in its own directory of the tree, so its name does not clash with a.txt, which it points at.
	var aID string
	for _, c := range decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/nodes/"+tree.ID+"/children", fx.key, nil)).Items {
		if c.Name == "a.txt" {
			aID = c.ID
		}
	}
	links := mkdirs(t, s, fx.key, f.ID, "/tree/links")
	wantStatus(t, call(t, s, "POST", "/api/v1/nodes/"+links.ID+"/shortcuts", fx.key, map[string]any{"target": aID}), 201)
	if chunked {
		s.copySyncNodes = 3
		s.copyBatchRows = 2
	} else {
		s.copySyncNodes = 1000
	}
	before := len(outboxOf(t, s, fx.acc.ID))
	rec := call(t, s, "POST", "/api/v1/nodes/"+tree.ID+"/copy", fx.key, map[string]any{"name": "copy"})
	if chunked {
		wantStatus(t, rec, 202)
		job := decode[jobJSON](t, rec)
		if err := s.jobs.RunPending(context.Background()); err != nil {
			t.Fatal(err)
		}
		if done := decode[jobJSON](t, call(t, s, "GET", "/api/v1/jobs/"+job.ID, fx.key, nil)); done.Status != "succeeded" {
			t.Fatalf("job = %+v", done)
		}
	} else {
		wantStatus(t, rec, 201)
	}
	created := 0
	for _, e := range outboxOf(t, s, fx.acc.ID)[before:] {
		if e.Type == "file.created" {
			created++
		}
	}
	return created
}

// TestChunkedCopyReportsWhatTheOneWriteCopyReports: the same tree, with a shortcut in it, makes the same file.created
// events whether it is copied in one write or as a chunked job. The one-write copy reports every non-folder node.
func TestChunkedCopyReportsWhatTheOneWriteCopyReports(t *testing.T) {
	t.Parallel()
	one := copyEventCount(t, false)
	chunked := copyEventCount(t, true)
	if one != 6 {
		t.Fatalf("one-write copy reported %d events, want 6 (five files and a shortcut)", one)
	}
	if chunked != one {
		t.Errorf("chunked copy reported %d events, the one-write copy %d: they must match", chunked, one)
	}
}
