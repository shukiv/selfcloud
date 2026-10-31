// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// restrictedCopy is a member, ann, who copies a tree of the folder she writes to. The tree is ann's to copy at subject
// role: the test sets it, and changes it mid-copy. The job runs in batches of two, so there is a change point between
// batches.
type restrictedCopy struct {
	m    *memberFixture
	tree nodeJSON
	job  jobJSON
}

// The tree is in folder g, which ann may read but not write, so her role on it is what the permission says (a write
// grant on the folder would raise it: inheritance only adds). The copy goes into folder f, which she writes to.
func newRestrictedCopy(t *testing.T, role string) *restrictedCopy {
	t.Helper()
	m := newMemberFixture(t)
	m.s.copySyncNodes = 3
	m.s.copyBatchRows = 2
	tree := bigTree(t, m.s, m.key, m.g.ID)
	if role != "viewer" {
		wantStatus(t, call(t, m.s, "PUT", "/api/v1/nodes/"+tree.ID+"/permissions/"+m.ann.ID, m.key,
			map[string]any{"role": role}), 200)
	}
	return &restrictedCopy{m: m, tree: tree}
}

// queue asks for the copy as ann, into folder f, and returns the job.
func (rc *restrictedCopy) queue(t *testing.T) jobJSON {
	t.Helper()
	rec := callSession(t, rc.m.s, rc.m.sess, "POST", "/api/v1/nodes/"+rc.tree.ID+"/copy",
		map[string]any{"name": "copy", "folder": rc.m.f.ID})
	wantStatus(t, rec, 202)
	rc.job = decode[jobJSON](t, rec)
	return rc.job
}

// setRole changes ann's role on the tree, as the account key.
func (rc *restrictedCopy) setRole(t *testing.T, role string) {
	t.Helper()
	wantStatus(t, call(t, rc.m.s, "PUT", "/api/v1/nodes/"+rc.tree.ID+"/permissions/"+rc.m.ann.ID, rc.m.key,
		map[string]any{"role": role}), 200)
}

// setNoDownload sets no_download on the tree, as the account key.
func (rc *restrictedCopy) setNoDownload(t *testing.T) {
	t.Helper()
	wantStatus(t, call(t, rc.m.s, "PATCH", "/api/v1/nodes/"+rc.tree.ID, rc.m.key,
		map[string]any{"no_download": true}), 200)
}

// assertFailedAndNothingVisible checks a failed copy: the job failed with wantErr in its error (the reason the test
// means to exercise, so a failure elsewhere does not pass), the folder lists what it listed before, no reservation is
// held, and the copy's row is gone.
func (rc *restrictedCopy) assertFailedAndNothingVisible(t *testing.T, before int, beforeUsage usageJSON, wantErr string) {
	t.Helper()
	done := decode[jobJSON](t, call(t, rc.m.s, "GET", "/api/v1/jobs/"+rc.job.ID, rc.m.key, nil))
	if done.Status != store.JobFailed {
		t.Fatalf("job = %+v, want failed", done)
	}
	if !strings.Contains(done.Error, wantErr) {
		t.Errorf("job error = %q, want it to contain %q", done.Error, wantErr)
	}
	if n := childCount(t, rc.m); n != before {
		t.Errorf("entries in the folder after the failed copy = %d, was %d", n, before)
	}
	u := usageOf(t, rc.m.s, rc.m.key, rc.m.acc.ID)
	if u.ReservedBytes != 0 || u.UsedBytes != beforeUsage.UsedBytes || u.UsedFiles != beforeUsage.UsedFiles {
		t.Errorf("usage after the failed copy = %+v, was %+v; want no reservation and no change", u, beforeUsage)
	}
	if _, err := rc.m.s.store.CopyJobOf(context.Background(), rc.job.ID); err == nil {
		t.Error("copy row still there after the failed copy")
	}
}

// TestNoDownloadSetMidCopyFailsTheCopy: a viewer's copy stops when the source becomes no_download between two batches,
// and nothing it staged is visible, the reservation is released, and the copy row is gone.
func TestNoDownloadSetMidCopyFailsTheCopy(t *testing.T) {
	t.Parallel()
	rc := newRestrictedCopy(t, "viewer")
	before := childCount(t, rc.m)
	beforeUsage := usageOf(t, rc.m.s, rc.m.key, rc.m.acc.ID)
	rc.queue(t)
	batches := 0
	rc.m.s.copyFault = func(stage string) error {
		if stage == "batch" {
			batches++
			if batches == 2 {
				rc.setNoDownload(t)
			}
		}
		return nil
	}
	if err := rc.m.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	rc.assertFailedAndNothingVisible(t, before, beforeUsage, "download_restricted")
}

// TestNoDownloadSetAtPublishFailsTheCopy: the same, with the source made no_download after the last batch and before
// the publish, so only the publish's own check can refuse it.
func TestNoDownloadSetAtPublishFailsTheCopy(t *testing.T) {
	t.Parallel()
	rc := newRestrictedCopy(t, "viewer")
	before := childCount(t, rc.m)
	beforeUsage := usageOf(t, rc.m.s, rc.m.key, rc.m.acc.ID)
	rc.queue(t)
	rc.m.s.copyFault = func(stage string) error {
		if stage == "publish" {
			rc.setNoDownload(t)
		}
		return nil
	}
	if err := rc.m.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	rc.assertFailedAndNothingVisible(t, before, beforeUsage, "download_restricted")
}

// TestManagerLostMidCopyFailsTheCopy: a manager's copy keeps restricted labels (copyKeepsRestricted). Its role drops to
// editor between two batches, so the later batches would not keep them: the copy fails, with nothing visible.
func TestManagerLostMidCopyFailsTheCopy(t *testing.T) {
	t.Parallel()
	rc := newRestrictedCopy(t, "manager")
	before := childCount(t, rc.m)
	beforeUsage := usageOf(t, rc.m.s, rc.m.key, rc.m.acc.ID)
	rc.queue(t)
	batches := 0
	rc.m.s.copyFault = func(stage string) error {
		if stage == "batch" {
			batches++
			if batches == 2 {
				rc.setRole(t, "editor")
			}
		}
		return nil
	}
	if err := rc.m.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	rc.assertFailedAndNothingVisible(t, before, beforeUsage, "restricted-label rule")
}

// TestManagerLostAtPublishFailsTheCopy: the role drops after the last batch, so the publish alone sees it.
func TestManagerLostAtPublishFailsTheCopy(t *testing.T) {
	t.Parallel()
	rc := newRestrictedCopy(t, "manager")
	before := childCount(t, rc.m)
	beforeUsage := usageOf(t, rc.m.s, rc.m.key, rc.m.acc.ID)
	rc.queue(t)
	rc.m.s.copyFault = func(stage string) error {
		if stage == "publish" {
			rc.setRole(t, "editor")
		}
		return nil
	}
	if err := rc.m.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	rc.assertFailedAndNothingVisible(t, before, beforeUsage, "restricted-label rule")
}

// TestManagerCopyKeepsRestrictedAndSucceeds: a manager's copy, with nothing changed, finishes. The first batch decides
// that its copies keep restricted values, and every later check agrees. Without this, a copy that failed at its first
// batch would pass the failure tests above.
func TestManagerCopyKeepsRestrictedAndSucceeds(t *testing.T) {
	t.Parallel()
	rc := newRestrictedCopy(t, "manager")
	rc.queue(t)
	if err := rc.m.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := decode[jobJSON](t, call(t, rc.m.s, "GET", "/api/v1/jobs/"+rc.job.ID, rc.m.key, nil))
	if done.Status != store.JobSucceeded {
		t.Fatalf("job = %+v, want succeeded", done)
	}
}
