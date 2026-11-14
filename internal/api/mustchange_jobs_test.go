// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// mustChangeJobFixture is a chunked-copy fixture with two members who may write the folder, and an
// installation admin with tenant access, so the admin can type a member's password while a copy runs.
type mustChangeJobFixture struct {
	*chunkedFixture
	adminSess testSession
	ann       userResponse
	annSess   testSession
	bob       userResponse
	bobSess   testSession
}

func newMustChangeJobFixture(t *testing.T) *mustChangeJobFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{}, func(c *config.Config) { c.Admin.TenantAccess = true })
	s.copySyncNodes = 3 // the 7-node tree is a job
	admin := adminKey(t, s)
	_, adminSess := newAdminUser(t, s, admin, "admin@example.com")
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	tree := bigTree(t, s, key, f.ID)
	fx := &mustChangeJobFixture{
		chunkedFixture: &chunkedFixture{s: s, admin: admin, acc: acc, key: key, f: f, tree: tree},
		adminSess:      adminSess,
	}
	fx.ann, fx.annSess = verifiedUser(t, s, key, acc.ID, "ann@example.com")
	fx.bob, fx.bobSess = verifiedUser(t, s, key, acc.ID, "bob@example.com")
	for _, usr := range []string{fx.ann.ID, fx.bob.ID} {
		wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+f.ID+"/grants/"+usr, key,
			map[string]any{"level": "write"}), 200)
	}
	return fx
}

// queueAs asks for the copy as a browser session and returns the job; nothing runs yet.
func (fx *mustChangeJobFixture) queueAs(t *testing.T, sess testSession) jobJSON {
	t.Helper()
	rec := callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", map[string]any{"name": "copy"})
	wantStatus(t, rec, 202)
	fx.job = decode[jobJSON](t, rec)
	return fx.job
}

// queueWith asks for the copy with a credential (a delegated token or the account key) and returns the job.
func (fx *mustChangeJobFixture) queueWith(t *testing.T, cred, name string) jobJSON {
	t.Helper()
	rec := call(t, fx.s, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy", cred, map[string]any{"name": name})
	wantStatus(t, rec, 202)
	fx.job = decode[jobJSON](t, rec)
	return fx.job
}

// typePassword has the admin type a member's password, which sets the must-change flag and ends the
// member's sessions and tokens.
func (fx *mustChangeJobFixture) typePassword(t *testing.T, usr, password string) {
	t.Helper()
	wantStatus(t, callSession(t, fx.s, fx.adminSess, "POST",
		"/api/v1/accounts/"+fx.acc.ID+"/users/"+usr+"/password",
		map[string]any{"password": password, "admin_password": testPassword}), 204)
}

// wantCopyRefusedGone checks that a copy job failed because its caller no longer has access.
func wantCopyRefusedGone(t *testing.T, j store.Job) {
	t.Helper()
	if j.Status != store.JobFailed || !strings.Contains(j.Error, "no longer has access") {
		t.Fatalf("job %s %q; want failed on the caller's access", j.Status, j.Error)
	}
}

// A copy queued by a browser session stops at its next batch once an admin types the caller's password
// between two batches: the job's caller must change it, so the copy is refused and nothing it staged
// becomes visible. The typed password ends the caller's sessions, but a job reads the delegation, not the
// session, so only the must-change rule can stop it here.
func TestCopyJobOfAMustChangeSessionStops(t *testing.T) {
	t.Parallel()
	fx := newMustChangeJobFixture(t)
	fx.s.copyBatchRows = 2
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	fx.queueAs(t, fx.annSess)
	batches := 0
	fx.s.copyFault = func(stage string) error {
		if stage == "batch" {
			batches++
			if batches == 2 { // after the first batch, before the second
				fx.typePassword(t, fx.ann.ID, "a password the admin typed")
			}
		}
		return nil
	}
	wantCopyRefusedGone(t, fx.run(t))
	fx.assertNothingVisible(t, before)
}

// A copy queued by a delegated token stops the same way. The flag is set in the store with no cut-off
// moved, so the token is still live and only the must-change rule can refuse the job: the API's typed
// password would also void the token, which the existing cut-off check already catches.
func TestCopyJobOfAMustChangeTokenStops(t *testing.T) {
	t.Parallel()
	fx := newMustChangeJobFixture(t)
	fx.s.copyBatchRows = 2
	tok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.f.ID}, "user": fx.ann.ID, "permissions": "write"})
	before := usageOf(t, fx.s, fx.key, fx.acc.ID)
	fx.queueWith(t, tok, "copy")
	batches := 0
	fx.s.copyFault = func(stage string) error {
		if stage == "batch" {
			batches++
			if batches == 2 {
				setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
			}
		}
		return nil
	}
	wantCopyRefusedGone(t, fx.run(t))
	fx.assertNothingVisible(t, before)
}

// While one member must change its password, another member's copy and the account key's copy still run:
// the rule binds the flagged user, not the account.
func TestCopyJobsOfOthersRunWhileOneMustChange(t *testing.T) {
	t.Parallel()
	fx := newMustChangeJobFixture(t)
	setMustChange(t, fx.s, fx.acc.ID, fx.ann.ID, true)
	fx.queueAs(t, fx.bobSess)
	if done := fx.run(t); done.Status != store.JobSucceeded {
		t.Fatalf("bob's job = %+v, want succeeded", done)
	}
	if n := fx.topLevel(t); n != 2 {
		t.Errorf("top-level after bob's copy = %d, want 2", n)
	}
	fx.queueWith(t, fx.key, "key copy")
	if done := fx.run(t); done.Status != store.JobSucceeded {
		t.Fatalf("the key's job = %+v, want succeeded", done)
	}
}

// After the caller changes the password it was given, a new copy it queues runs. While the flag is set
// the caller's session may not queue a copy at all; the change clears the flag and the next job is live.
func TestCopyJobRunsAfterTheCallerChangesPassword(t *testing.T) {
	t.Parallel()
	fx := newMustChangeJobFixture(t)
	fx.typePassword(t, fx.ann.ID, "a password the admin typed")
	rec := login(t, fx.s, "ann@example.com", "a password the admin typed", "")
	wantStatus(t, rec, 200)
	sess := sessionFrom(t, fx.s, rec)
	// The must-change session is refused the copy route.
	wantMustChangeRefusal(t, callSession(t, fx.s, sess, "POST", "/api/v1/nodes/"+fx.tree.ID+"/copy",
		map[string]any{"name": "copy"}))
	// The caller changes the password, which clears the flag, and signs in with the new one.
	wantStatus(t, callSession(t, fx.s, sess, "POST", "/api/v1/me/password",
		map[string]any{"current_password": "a password the admin typed", "new_password": "ann's own password"}), 204)
	rec = login(t, fx.s, "ann@example.com", "ann's own password", "")
	wantStatus(t, rec, 200)
	sess = sessionFrom(t, fx.s, rec)
	fx.queueAs(t, sess)
	if done := fx.run(t); done.Status != store.JobSucceeded {
		t.Fatalf("job after the change = %+v, want succeeded", done)
	}
	if n := fx.topLevel(t); n != 2 {
		t.Errorf("top-level after the copy = %d, want 2", n)
	}
}
