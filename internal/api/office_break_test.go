// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// lockRowFor takes a live lock on the file nodeID of accID for holder, in the store, as an editor's LOCK does. A test
// uses it to hold a file without an editor.
func lockRowFor(t *testing.T, s *Server, accID, nodeID, lockID, holder string) {
	t.Helper()
	now := s.store.Clock()
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		_, err := tx.PutLock(context.Background(), &store.WOPILock{NodeID: nodeID, AccountID: accID, LockID: lockID,
			Holder: holder, CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// liveLockID is the ID of the live lock on nodeID, or "" when no editor holds the file.
func liveLockID(t *testing.T, s *Server, accID, nodeID string) string {
	t.Helper()
	l, err := s.store.LiveLock(context.Background(), accID, nodeID)
	if errors.Is(err, store.ErrNotFound) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return l.LockID
}

// clearLock ends the live lock on nodeID, if there is one, so the next case starts with the file free.
func clearLock(t *testing.T, s *Server, accID, nodeID string) {
	t.Helper()
	err := s.store.Write(context.Background(), func(tx *store.Tx) error {
		_, err := tx.BreakLock(context.Background(), accID, nodeID)
		return err
	})
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}

// breakPath is the route that ends a file's lock.
func breakPath(nodeID string) string { return "/api/v1/nodes/" + nodeID + "/unlock" }

// lastBreak returns the newest node.unlock audit entry of the account that records a broken lock, or nil.
func lastBreak(t *testing.T, s *Server, key, accID string) *auditJSON {
	t.Helper()
	rec := call(t, s, "GET", "/api/v1/accounts/"+accID+"/audit", key, nil)
	wantStatus(t, rec, 200)
	entries := decode[struct {
		Items []auditJSON `json:"items"`
	}](t, rec).Items
	// The list is newest first, so the first match is the latest break.
	for i := range entries {
		e := entries[i]
		if e.Action == "node.unlock" && e.Details["reason"] == "broken" {
			return &e
		}
	}
	return nil
}

// TestBreakLockAuthorisationByCallerKind: a manager of the file, an owner of the account and the account key may end a
// lock; any other caller is refused, and a refused break leaves the lock as it was. A delegated token is refused even
// for a manager, since no token breaks a lock.
func TestBreakLockAuthorisationByCallerKind(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.deep.ID, fx.ann.ID, "manager")
	fx.setPerm(t, fx.deep.ID, fx.bob.ID, "editor")
	carol, carolSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "carol@example.com")
	fx.setPerm(t, fx.deep.ID, carol.ID, "viewer")
	_, danSess := verifiedUser(t, fx.s, fx.key, fx.acc.ID, "dan@example.com")
	owner, ownerSess := ownerUser(t, fx.s, fx.key, fx.acc.ID, "owner@example.com")
	_, otherKey := newAccount(t, fx.s, fx.admin, "Other")
	// A write token of a manager: its role and its permissions both allow the break, so only the token refusal stops it.
	mgrToken := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID},
		"permissions": "write"})
	path := breakPath(fx.deep.ID)
	cases := []struct {
		name  string
		do    func() *httptest.ResponseRecorder
		want  int
		actor string // the audit actor's kind when the break succeeds
		id    string // the audit actor's ID for a user; "" for the key
	}{
		{"account key", func() *httptest.ResponseRecorder { return call(t, fx.s, "POST", path, fx.key, nil) }, 204,
			string(auth.KindAccountKey), ""},
		{"manager", func() *httptest.ResponseRecorder { return callSession(t, fx.s, fx.annSess, "POST", path, nil) }, 204,
			string(auth.KindUser), fx.ann.ID},
		{"owner of the account", func() *httptest.ResponseRecorder { return callSession(t, fx.s, ownerSess, "POST", path, nil) },
			204, string(auth.KindUser), owner.ID},
		{"editor", func() *httptest.ResponseRecorder { return callSession(t, fx.s, fx.bobSess, "POST", path, nil) }, 403, "", ""},
		{"viewer", func() *httptest.ResponseRecorder { return callSession(t, fx.s, carolSess, "POST", path, nil) }, 403, "", ""},
		{"no role on the file", func() *httptest.ResponseRecorder { return callSession(t, fx.s, danSess, "POST", path, nil) }, 404, "", ""},
		{"another account's key", func() *httptest.ResponseRecorder { return call(t, fx.s, "POST", path, otherKey, nil) }, 404, "", ""},
		{"installation admin key", func() *httptest.ResponseRecorder { return call(t, fx.s, "POST", path, fx.admin, nil) }, 403, "", ""},
		{"no credential", func() *httptest.ResponseRecorder { return call(t, fx.s, "POST", path, "", nil) }, 401, "", ""},
		{"delegated token of a manager", func() *httptest.ResponseRecorder { return call(t, fx.s, "POST", path, mgrToken, nil) }, 403, "", ""},
	}
	for i, c := range cases {
		lockID := fmt.Sprintf("lock-%d", i)
		lockRowFor(t, fx.s, fx.acc.ID, fx.deep.ID, lockID, "usr_holder")
		rec := c.do()
		if rec.Code != c.want {
			t.Fatalf("%s: status %d, want %d: %s", c.name, rec.Code, c.want, rec.Body.String())
		}
		if c.want != 204 {
			if got := liveLockID(t, fx.s, fx.acc.ID, fx.deep.ID); got != lockID {
				t.Fatalf("%s: a refused break left the lock %q, want %q", c.name, got, lockID)
			}
			clearLock(t, fx.s, fx.acc.ID, fx.deep.ID)
			continue
		}
		if got := liveLockID(t, fx.s, fx.acc.ID, fx.deep.ID); got != "" {
			t.Fatalf("%s: a break left the lock %q", c.name, got)
		}
		e := lastBreak(t, fx.s, fx.key, fx.acc.ID)
		if e == nil {
			t.Fatalf("%s: no node.unlock entry with reason broken", c.name)
		}
		if e.Actor.Kind != c.actor || (c.id != "" && e.Actor.ID != c.id) {
			t.Fatalf("%s: audit actor %+v, want kind %q id %q", c.name, e.Actor, c.actor, c.id)
		}
		if e.Target != fx.deep.ID {
			t.Fatalf("%s: audit target %q, want %q", c.name, e.Target, fx.deep.ID)
		}
	}
}

// TestBreakLockRefusesAShareLinkVisitor: a visitor of a share link is refused. An anyone link gives the viewer role only,
// so the role refuses it; the route refuses the visitor's kind as well.
func TestBreakLockRefusesAShareLinkVisitor(t *testing.T) {
	t.Parallel()
	fx := newAnyoneFixture(t)
	_, secret := fx.makeAnyone(t, fx.deep.ID, nil)
	visitor := fx.visitAnyone(t, secret, "")
	lockRowFor(t, fx.s, fx.acc.ID, fx.deep.ID, "lock-visitor", "usr_holder")
	wantStatus(t, call(t, fx.s, "POST", breakPath(fx.deep.ID), visitor, nil), 403)
	if got := liveLockID(t, fx.s, fx.acc.ID, fx.deep.ID); got != "lock-visitor" {
		t.Fatalf("a visitor's break left the lock %q", got)
	}
}

// TestBreakLockRefusesAWOPIToken: the editor's own WOPI token cannot end its lock through the API. Only the WOPI routes
// take a WOPI token, and a break is not one of them.
func TestBreakLockRefusesAWOPIToken(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "L1")
	rec := call(t, fx.s, "POST", breakPath(fx.doc.ID), tok, nil)
	if rec.Code < 400 {
		t.Fatalf("a WOPI token broke a lock: status %d", rec.Code)
	}
	if got := liveLockID(t, fx.s, fx.acc.ID, fx.doc.ID); got != "L1" {
		t.Fatalf("a WOPI token's break left the lock %q, want L1", got)
	}
}

// TestBreakLockHolderIsRefusedAfterwards: once a manager has broken the editor's lock, the editor's refresh and save
// answer with no lock on the file, and neither overwrites what came after the break.
func TestBreakLockHolderIsRefusedAfterwards(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "OLD")
	wantStatus(t, call(t, fx.s, "POST", breakPath(fx.doc.ID), fx.key, nil), 204)
	saveRoute := nodeRoute(fx.doc.ID) + "/contents"
	wantLockAnswer(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("editor"), saveHdr("OLD")), "")
	wantLockAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, wopiLockHdr("REFRESH_LOCK", "OLD")), "")
	if got := fx.fileText(t, fx.doc.ID); got != "hello" {
		t.Fatalf("the holder's refused save changed the file to %q", got)
	}
	// Someone else writes the file through the API. The holder's next save must not overwrite it.
	wantStatus(t, apiPut(t, fx.s, fx.key, fx.doc.ID, fx.currentETag(t, fx.doc.ID), "after the break"), 200)
	wantLockAnswer(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("editor again"), saveHdr("OLD")), "")
	if got := fx.fileText(t, fx.doc.ID); got != "after the break" {
		t.Fatalf("the holder overwrote a write made after the break: %q", got)
	}
}

// TestBreakLockNeedsALockedFile: a break of a file that no editor holds is a conflict, and a folder has no lock to break.
func TestBreakLockNeedsALockedFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	wantError(t, call(t, fx.s, "POST", breakPath(fx.doc.ID), fx.key, nil), 409, apierr.Conflict)
	wantError(t, call(t, fx.s, "POST", breakPath(fx.fld.ID), fx.key, nil), 404, apierr.NotFound)
}

// TestUserDeletionReleasesItsOpenFile: a user whose personal folder holds a file open in an editor is deleted with its
// files, and the lock goes with the user (decision 32). The deletion is not refused, nor left to a later retry.
func TestUserDeletionReleasesItsOpenFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	pf, err := fx.s.store.PersonalFolder(context.Background(), fx.acc.ID, fx.ann.ID)
	if err != nil {
		t.Fatal(err)
	}
	mine := putNode(t, fx.s, fx.key, pf.ID, "/mine.docx", "mine")
	lockRowFor(t, fx.s, fx.acc.ID, mine.ID, "L-mine", fx.ann.ID)
	wantStatus(t, call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID+"?delete_files=true",
		fx.key, nil), 204)
	if got := liveLockID(t, fx.s, fx.acc.ID, mine.ID); got != "" {
		t.Fatalf("the deleted user's file kept the lock %q", got)
	}
}

// TestAccountDeletionWithAnOpenFileFinishesAtOnce: an account with a file open in an editor is deleted by its first job
// run. The job clears the locks in its own write, so nothing fails and nothing waits for the lock to expire (decision 32).
func TestAccountDeletionWithAnOpenFileFinishesAtOnce(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	lockRowFor(t, fx.s, fx.acc.ID, fx.doc.ID, "L-open", fx.ann.ID)
	rec := call(t, fx.s, "DELETE", "/api/v1/accounts/"+fx.acc.ID, fx.admin, nil)
	wantStatus(t, rec, 202)
	job := decode[jobJSON](t, rec)
	if err := fx.s.jobs.RunPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := decode[jobJSON](t, call(t, fx.s, "GET", "/api/v1/jobs/"+job.ID, fx.admin, nil))
	if got.Status != "succeeded" {
		t.Fatalf("account job after one run = %+v, want succeeded", got)
	}
	wantError(t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID, fx.admin, nil), 404, apierr.NotFound)
}
