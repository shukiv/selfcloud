// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestCopyJobQuotaFailureHasNoNumbers: a chunked copy into a member's My files passes its check at the request, and the
// member's limit then falls below the copy's bytes before the publish. The refusal is stored on the job, which an owner
// session can read through GET /jobs, so the stored text carries the code and no numbers. The member's free space is for
// the member and the key to learn (design section 8, "Who learns the numbers").
func TestCopyJobQuotaFailureHasNoNumbers(t *testing.T) {
	t.Parallel()
	fx := newP1Fixture(t)
	fx.s.copySyncNodes = 3
	src := newFolder(t, fx.s, fx.key, fx.acc.ID, "Src")
	tree := bigTree(t, fx.s, fx.key, src.ID) // five one-byte files
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+tree.ID+"/copy", fx.key,
		map[string]any{"folder": fx.annPF, "name": "copy"})
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)
	fx.s.copyFault = func(stage string) error {
		if stage == "publish" {
			wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
				map[string]any{"quota_bytes": 3}), 200)
		}
		return nil
	}
	if err := fx.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	done, err := fx.s.store.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != store.JobFailed {
		t.Fatalf("job = %+v, want failed at the publish", done)
	}
	if !strings.HasPrefix(done.Error, "quota_exceeded") {
		t.Fatalf("job error = %q, want the quota refusal", done.Error)
	}
	if digitsRE.MatchString(done.Error) {
		t.Errorf("job error = %q carries numbers, which an owner session can read", done.Error)
	}
}

// TestShortcutLinkRefusalNamesAShortcut: a link to a shortcut is refused as invalid_request, and the message says a
// shortcut cannot be linked. It must not say a folder cannot be linked: a shortcut is not a folder, and its target
// is the node a link can name (design section 10, shortcuts: "A shortcut cannot be shared by link. Its target can.").
func TestShortcutLinkRefusalNamesAShortcut(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	sc := makeShortcut(t, fx, fx.c.ID, fx.deep.ID)
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+sc["id"].(string)+"/links", fx.key,
		map[string]any{"disposition": "inline", "ttl": 60})
	e := wantError(t, rec, 400, apierr.InvalidRequest)
	if strings.Contains(e.Message, "folder") || !strings.Contains(e.Message, "shortcut") {
		t.Errorf("message = %q, want it to name the shortcut, not a folder", e.Message)
	}
}
