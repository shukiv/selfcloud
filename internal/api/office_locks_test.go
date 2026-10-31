// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
	"github.com/shukiv/selfcloud/internal/sweep"
)

// lockClock is the store's clock in these tests. It moves only when a test moves it.
type lockClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *lockClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *lockClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// useLockClock puts the fixture's store on a fake clock that starts now. Proof timestamps and token expiries read the
// same clock, so the fake clock governs the whole WOPI path.
func (fx *officeFixture) useLockClock(t *testing.T) *lockClock {
	t.Helper()
	c := &lockClock{t: time.Now()}
	fx.s.store.SetClock(c.now)
	return c
}

// wopiLockHdr is a WOPI lock operation's headers.
func wopiLockHdr(op, lockID string) map[string]string {
	return map[string]string{"X-WOPI-Override": op, "X-WOPI-Lock": lockID}
}

// saveHdr is a PutFile's headers, with the lock ID it sends ("" for none).
func saveHdr(lockID string) map[string]string {
	h := map[string]string{"X-WOPI-Override": "PUT"}
	if lockID != "" {
		h["X-WOPI-Lock"] = lockID
	}
	return h
}

// wantLockAnswer asks a WOPI answer to be a 409 whose X-WOPI-Lock is current ("" when unlocked).
func wantLockAnswer(t *testing.T, rec *httptest.ResponseRecorder, current string) {
	t.Helper()
	wantStatus(t, rec, 409)
	got, ok := rec.Result().Header["X-Wopi-Lock"]
	if !ok || len(got) != 1 || got[0] != current {
		t.Fatalf("X-WOPI-Lock = %q (present %v), want %q", got, ok, current)
	}
	if rec.Header().Get("X-WOPI-LockFailureReason") == "" {
		t.Errorf("a 409 gave no X-WOPI-LockFailureReason")
	}
}

// fileText reads the file's content through the API, as its owner.
func (fx *officeFixture) fileText(t *testing.T, nodeID string) string {
	t.Helper()
	return call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID+"/content", fx.key, nil).Body.String()
}

// apiPut replaces a file's content through the API, as the account key, with the ETag the caller read.
func apiPut(t *testing.T, s *Server, key, nodeID, ifMatch, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/v1/nodes/"+nodeID+"/content", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("If-Match", ifMatch)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// currentETag reads a file's ETag through the API.
func (fx *officeFixture) currentETag(t *testing.T, nodeID string) string {
	t.Helper()
	return decode[nodeJSON](t, call(t, fx.s, "GET", "/api/v1/nodes/"+nodeID, fx.key, nil)).ETag
}

// TestWOPILockAnswersFollowTheSpecification: Lock, GetLock, RefreshLock and Unlock answer as the WOPI specification
// says, and the lock ID is checked at the boundary.
func TestWOPILockAnswersFollowTheSpecification(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	annTok := fx.openToken(t, "edit")
	fx.grant(t, fx.doc.ID, fx.bob.ID, "editor")
	bobTok := decode[officeJSON](t, fx.open(t, fx.bobSess, fx.doc.ID, "edit")).Token
	docRoute := nodeRoute(fx.doc.ID)

	// The lock ID is checked at the boundary: present, at most 1024 characters, printable ASCII.
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("LOCK", "")), 400)
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, annTok, nil,
		wopiLockHdr("LOCK", strings.Repeat("a", 1025))), 400)
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("LOCK", "café")), 400)
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("LOCK", "tab\tin")), 400)

	// Granted: 200, and no X-WOPI-Lock on a success.
	rec := fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("LOCK", "L1"))
	wantStatus(t, rec, 200)
	if _, ok := rec.Result().Header["X-Wopi-Lock"]; ok {
		t.Errorf("a 200 for Lock carries X-WOPI-Lock; the specification says it should not")
	}
	// Another editor's new ID: 409 with the current lock.
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute, bobTok, nil, wopiLockHdr("LOCK", "L2")), "L1")
	// The same ID from another editor is a refresh: the lock is not owned by a person.
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, bobTok, nil, wopiLockHdr("LOCK", "L1")), 200)
	// GetLock names the lock, and changes nothing.
	got := fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("GET_LOCK", ""))
	wantStatus(t, got, 200)
	if v := got.Result().Header["X-Wopi-Lock"]; len(v) != 1 || v[0] != "L1" {
		t.Errorf("GetLock X-WOPI-Lock = %v, want L1", v)
	}
	// RefreshLock and Unlock with another ID are mismatches, answered with the current lock.
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute, bobTok, nil, wopiLockHdr("REFRESH_LOCK", "L2")), "L1")
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute, bobTok, nil, wopiLockHdr("UNLOCK", "L2")), "L1")
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("UNLOCK", "L1")), 200)

	// Unlocked: GetLock answers "", and Unlock or RefreshLock answer 409 with "".
	empty := fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("GET_LOCK", ""))
	wantStatus(t, empty, 200)
	if v := empty.Result().Header["X-Wopi-Lock"]; len(v) != 1 || v[0] != "" {
		t.Errorf("GetLock on an unlocked file X-WOPI-Lock = %v, want the empty string", v)
	}
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("UNLOCK", "L1")), "")
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("REFRESH_LOCK", "L1")), "")

	// A viewer may not lock: the specification's "user unauthorized" is a 404.
	fx.grant(t, fx.doc.ID, fx.bob.ID, "viewer")
	viewTok := decode[officeJSON](t, fx.open(t, fx.bobSess, fx.doc.ID, "view")).Token
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, viewTok, nil, wopiLockHdr("LOCK", "L9")), 404)

	// Each lock operation is audited, and the file's content is unchanged by them.
	entries := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	seen := map[string]bool{}
	for _, a := range entries.Items {
		if a.Target == fx.doc.ID && (a.Action == "node.lock" || a.Action == "node.unlock") {
			seen[a.Action] = true
		}
	}
	if !seen["node.lock"] || !seen["node.unlock"] {
		t.Errorf("audit entries for the lock = %v, want node.lock and node.unlock", seen)
	}
	if got := fx.fileText(t, fx.doc.ID); got != "hello" {
		t.Errorf("lock operations changed the file to %q", got)
	}
}

// TestWOPISaveNeedsTheLockOnTheFile: a PutFile without a lock, or with another lock, is refused and never overwrites;
// an empty file takes a save without a lock (document creation).
func TestWOPISaveNeedsTheLockOnTheFile(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	saveRoute := nodeRoute(fx.doc.ID) + "/contents"

	wantLockAnswer(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("no lock"), saveHdr("")), "")
	fx.lockFor(t, tok, "L1")
	wantLockAnswer(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("wrong"), saveHdr("L2")), "L1")
	if got := fx.fileText(t, fx.doc.ID); got != "hello" {
		t.Fatalf("a refused save changed the file to %q", got)
	}
	wantStatus(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader("mine"), saveHdr("L1")), 200)
	if got := fx.fileText(t, fx.doc.ID); got != "mine" {
		t.Errorf("the holder's save left %q", got)
	}

	// An empty file that no editor holds takes the first save without a lock.
	empty := putNode(t, fx.s, fx.key, fx.fld.ID, "/empty.docx", "")
	fx.grant(t, empty.ID, fx.ann.ID, "editor")
	rec := fx.open(t, fx.annSess, empty.ID, "edit")
	wantStatus(t, rec, 200)
	etok := decode[officeJSON](t, rec).Token
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(empty.ID)+"/contents", etok, strings.NewReader("created"),
		saveHdr("")), 200)
	if got := fx.fileText(t, empty.ID); got != "created" {
		t.Errorf("document creation left %q", got)
	}
}

// TestWOPIAnAPIWriteWaitsForTheLock: while an editor holds the file, every API change to it answers locked, and the
// same change goes through once the lock is gone.
func TestWOPIAnAPIWriteWaitsForTheLock(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "L1")

	wantError(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"name": "x.docx"}),
		409, apierr.Locked)
	wantError(t, apiPut(t, fx.s, fx.key, fx.doc.ID, fx.doc.ETag, "api write"), 409, apierr.Locked)
	if got := fx.fileText(t, fx.doc.ID); got != "hello" {
		t.Fatalf("an API write under the lock changed the file to %q", got)
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, wopiLockHdr("UNLOCK", "L1")), 200)
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"name": "x.docx"}), 200)
}

// TestWOPIStaleEditorCannotLock: an API write made after an editor opened the file, and before it locked, is not
// overwritten: the editor's lock is refused, and its save with no lock is refused too. A new open takes the lock.
func TestWOPIStaleEditorCannotLock(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	stale := fx.openToken(t, "edit") // the editor is given "hello"
	wantStatus(t, apiPut(t, fx.s, fx.key, fx.doc.ID, fx.doc.ETag, "changed"), 200)

	wantLockAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), stale, nil, wopiLockHdr("LOCK", "L1")), "")
	wantLockAnswer(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", stale, strings.NewReader("stale"),
		saveHdr("")), "")
	if got := fx.fileText(t, fx.doc.ID); got != "changed" {
		t.Fatalf("a stale editor overwrote the API write: %q", got)
	}

	// Opened again, the editor is given "changed", and locks and saves.
	fresh := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "edit")).Token
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), fresh, nil, wopiLockHdr("LOCK", "L2")), 200)
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", fresh, strings.NewReader("mine"),
		saveHdr("L2")), 200)
}

// TestWOPITwoEditorsNeverOverwrite: one editor holds the file; the other is refused the lock and the save. When the
// first has saved and unlocked, the second, which holds the content before that save, cannot lock: it must reopen.
func TestWOPITwoEditorsNeverOverwrite(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	annTok := fx.openToken(t, "edit")
	fx.grant(t, fx.doc.ID, fx.bob.ID, "editor")
	bobTok := decode[officeJSON](t, fx.open(t, fx.bobSess, fx.doc.ID, "edit")).Token
	docRoute := nodeRoute(fx.doc.ID)

	wantStatus(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("LOCK", "LA")), 200)
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute, bobTok, nil, wopiLockHdr("LOCK", "LB")), "LA")
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute+"/contents", bobTok, strings.NewReader("bobs"), saveHdr("LB")), "LA")
	wantStatus(t, fx.wopiCall(t, "POST", docRoute+"/contents", annTok, strings.NewReader("anns"), saveHdr("LA")), 200)
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, annTok, nil, wopiLockHdr("UNLOCK", "LA")), 200)

	// Bob's session holds "hello", not "anns": its lock is refused, and so is its save.
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute, bobTok, nil, wopiLockHdr("LOCK", "LB")), "")
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute+"/contents", bobTok, strings.NewReader("bobs"), saveHdr("LB")), "")
	if got := fx.fileText(t, fx.doc.ID); got != "anns" {
		t.Errorf("the file is %q, want the first editor's save", got)
	}
}

// TestWOPIConcurrentLocksOneWins: two editors ask for the lock at the same moment. The store serializes the two
// writes, so exactly one is granted, every round.
func TestWOPIConcurrentLocksOneWins(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	annTok := fx.openToken(t, "edit")
	fx.grant(t, fx.doc.ID, fx.bob.ID, "editor")
	bobTok := decode[officeJSON](t, fx.open(t, fx.bobSess, fx.doc.ID, "edit")).Token
	docRoute := nodeRoute(fx.doc.ID)
	for round := range 20 {
		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i, tok := range []string{annTok, bobTok} {
			wg.Add(1)
			go func(i int, tok string) {
				defer wg.Done()
				rec := httptest.NewRecorder()
				fx.s.Handler().ServeHTTP(rec, fx.wopiReq("POST", docRoute, tok, nil,
					wopiLockHdr("LOCK", "R"+string(rune('a'+i)))))
				codes[i] = rec.Code
			}(i, tok)
		}
		wg.Wait()
		granted := 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				granted++
			case http.StatusConflict:
			default:
				t.Fatalf("round %d: a concurrent Lock answered %d", round, c)
			}
		}
		if granted != 1 {
			t.Fatalf("round %d: %d locks granted, want exactly one (codes %v)", round, granted, codes)
		}
		// Whichever editor was granted ends the lock, so the next round starts unlocked.
		holder, lockID := bobTok, "Rb"
		if codes[0] == http.StatusOK {
			holder, lockID = annTok, "Ra"
		}
		wantStatus(t, fx.wopiCall(t, "POST", docRoute, holder, nil, wopiLockHdr("UNLOCK", lockID)), 200)
	}
}

// TestWOPIRefreshAgainstUnlockRace: a refresh and an unlock of the same lock race. Each answers 200 or 409, and once
// both are done the file is unlocked, because a refresh cannot bring back a lock that has been ended.
func TestWOPIRefreshAgainstUnlockRace(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	tok := fx.openToken(t, "edit")
	docRoute := nodeRoute(fx.doc.ID)
	for round := range 20 {
		fx.lockFor(t, tok, "RU")
		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i, op := range []string{"REFRESH_LOCK", "UNLOCK"} {
			wg.Add(1)
			go func(i int, op string) {
				defer wg.Done()
				rec := httptest.NewRecorder()
				fx.s.Handler().ServeHTTP(rec, fx.wopiReq("POST", docRoute, tok, nil, wopiLockHdr(op, "RU")))
				codes[i] = rec.Code
			}(i, op)
		}
		wg.Wait()
		for _, c := range codes {
			if c != http.StatusOK && c != http.StatusConflict {
				t.Fatalf("round %d: refresh or unlock answered %d (codes %v)", round, c, codes)
			}
		}
		if codes[1] != http.StatusOK {
			t.Fatalf("round %d: the unlock of the held lock was refused (codes %v)", round, codes)
		}
		rec := fx.wopiCall(t, "POST", docRoute, tok, nil, wopiLockHdr("GET_LOCK", ""))
		if v := rec.Result().Header["X-Wopi-Lock"]; len(v) != 1 || v[0] != "" {
			t.Fatalf("round %d: after refresh and unlock the file is still locked by %v", round, v)
		}
	}
}

// TestWOPILockExpiresWithoutARefresh: a lock that its editor stops refreshing ends 30 minutes after its last refresh,
// on the store's clock. The API may write once it has ended, and a save under it is refused, and the sweep audits it.
func TestWOPILockExpiresWithoutARefresh(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	clock := fx.useLockClock(t)
	tok := fx.openToken(t, "edit")
	docRoute := nodeRoute(fx.doc.ID)
	fx.lockFor(t, tok, "LE") // t=0: expires at 30 minutes

	clock.add(29 * time.Minute)
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, tok, nil, wopiLockHdr("REFRESH_LOCK", "LE")), 200) // expires at 59
	clock.add(29 * time.Minute)                                                                       // t=58
	wantStatus(t, fx.wopiCall(t, "POST", docRoute+"/contents", tok, strings.NewReader("still"), saveHdr("LE")), 200)
	clock.add(2 * time.Minute) // t=60: past the refreshed expiry, the editor has crashed
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/nodes/"+fx.doc.ID, fx.key, map[string]any{"name": "after.docx"}), 200)
	wantLockAnswer(t, fx.wopiCall(t, "POST", docRoute+"/contents", tok, strings.NewReader("late"), saveHdr("LE")), "")

	sw := sweep.New(sweep.Options{Store: fx.s.store, Blobs: fx.s.blobs, DeleteGrace: time.Hour})
	res, err := sw.Once(t.Context(), clock.now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Locks != 1 {
		t.Errorf("sweep removed %d expired locks, want 1 (the crashed editor's)", res.Locks)
	}
	entries := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	expired := false
	for _, a := range entries.Items {
		if a.Action == "node.unlock" && a.Target == fx.doc.ID {
			expired = true
		}
	}
	if !expired {
		t.Errorf("the expiry of the lock is not audited as node.unlock")
	}
}

// lockClockAdvance is a request body that moves the fake clock past the lock's expiry on its first read: the lock ends
// while the body is arriving.
type lockClockAdvance struct {
	clock *lockClock
	r     io.Reader
	done  bool
}

func (b *lockClockAdvance) Read(p []byte) (int, error) {
	if !b.done {
		b.done = true
		b.clock.add(31 * time.Minute)
	}
	return b.r.Read(p)
}

// TestWOPILockExpiringDuringASave: the lock is live when the save starts and has expired when the write is made. The
// save is refused, and the file keeps its content.
func TestWOPILockExpiringDuringASave(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	clock := fx.useLockClock(t)
	tok := fx.openToken(t, "edit")
	fx.lockFor(t, tok, "LX")
	body := &lockClockAdvance{clock: clock, r: strings.NewReader("too late")}
	req := fx.wopiReq("POST", nodeRoute(fx.doc.ID)+"/contents", tok, body, saveHdr("LX"))
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, req)
	if !body.done {
		t.Fatal("the test did not move the clock during the save")
	}
	wantLockAnswer(t, rec, "")
	if got := fx.fileText(t, fx.doc.ID); got != "hello" {
		t.Errorf("a save whose lock expired during it changed the file to %q", got)
	}
}

// TestWOPIDisabledUsersLockLapses: when a user is disabled, the editor's calls are refused at once, but the lock it
// holds stays until it expires: no other writer gets in before then.
func TestWOPIDisabledUsersLockLapses(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	clock := fx.useLockClock(t)
	annTok := fx.openToken(t, "edit")
	fx.lockFor(t, annTok, "LD")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+fx.ann.ID, fx.key,
		map[string]any{"status": "disabled"}), 200)

	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID)+"/contents", annTok, strings.NewReader("x"), saveHdr("LD")), 401)
	wantError(t, apiPut(t, fx.s, fx.key, fx.doc.ID, fx.currentETag(t, fx.doc.ID), "api"), 409, apierr.Locked)
	clock.add(31 * time.Minute)
	wantStatus(t, apiPut(t, fx.s, fx.key, fx.doc.ID, fx.currentETag(t, fx.doc.ID), "api"), 200)
}

// TestWOPIOneVersionPerLockSession: the saves of one editing session keep one version, the content from before its
// first save. A new session (a new lock) keeps its own. Autosaves do not evict the older version, and the quota is
// charged for the content that is kept and the current content, not for every save.
func TestWOPIOneVersionPerLockSession(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t, func(c *config.Config) { c.Versions.MaxCount = 2 })
	tok := fx.openToken(t, "edit")
	saveRoute := nodeRoute(fx.doc.ID) + "/contents"

	fx.lockFor(t, tok, "S1")
	for _, body := range []string{"one", "two", "three", "four", "five"} {
		wantStatus(t, fx.wopiCall(t, "POST", saveRoute, tok, strings.NewReader(body), saveHdr("S1")), 200)
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok, nil, wopiLockHdr("UNLOCK", "S1")), 200)

	versions, err := fx.s.store.Versions(t.Context(), fx.acc.ID, fx.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("five autosaves kept %d versions, want 1 (the session's)", len(versions))
	}
	first := versions[0].ID
	if got := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/versions/"+first+"/content", fx.key, nil).Body.String(); got != "hello" {
		t.Errorf("the session's version holds %q, want the content from before its first save", got)
	}

	// A second session keeps its own version, and its autosaves do not evict the first.
	tok2 := decode[officeJSON](t, fx.open(t, fx.annSess, fx.doc.ID, "edit")).Token
	fx.lockFor(t, tok2, "S2")
	for _, body := range []string{"six", "seven"} {
		wantStatus(t, fx.wopiCall(t, "POST", saveRoute, tok2, strings.NewReader(body), saveHdr("S2")), 200)
	}
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tok2, nil, wopiLockHdr("UNLOCK", "S2")), 200)
	versions, err = fx.s.store.Versions(t.Context(), fx.acc.ID, fx.doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("two sessions kept %d versions, want 2", len(versions))
	}
	if got := call(t, fx.s, "GET", "/api/v1/nodes/"+fx.doc.ID+"/versions/"+first+"/content", fx.key, nil).Body.String(); got != "hello" {
		t.Errorf("an autosave evicted the older session's version: now %q", got)
	}

	// Usage: the two kept versions ("hello", 5 bytes, and "five", 4 bytes) plus the current "seven" (5 bytes).
	acc := decode[accountJSON](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID, fx.admin, nil))
	if acc.UsedBytes != 14 {
		t.Errorf("used_bytes = %d, want 14 (5 + 4 kept, 5 current): the saves charged the quota for every save", acc.UsedBytes)
	}

	// A manager can still restore across the session: the content from before the first session's save comes back.
	restore := httptest.NewRequest("POST", "/api/v1/nodes/"+fx.doc.ID+"/versions/"+first+"/restore", nil)
	restore.Header.Set("Authorization", "Bearer "+fx.key)
	restore.Header.Set("If-Match", fx.currentETag(t, fx.doc.ID))
	rec := httptest.NewRecorder()
	fx.s.Handler().ServeHTTP(rec, restore)
	wantStatus(t, rec, 200)
	if got := fx.fileText(t, fx.doc.ID); got != "hello" {
		t.Errorf("restore across the sessions gave %q, want hello", got)
	}
}

// TestWOPILockOnAnotherAccountIsNotFound: an editor of one account cannot lock a file of another, and the other's
// file stays unlocked.
func TestWOPILockOnAnotherAccountIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	other, keyB := newAccount(t, fx.s, fx.admin, "Other")
	fldB := newFolder(t, fx.s, keyB, other.ID, "Team")
	fileB := putNode(t, fx.s, keyB, fldB.ID, "/mine.docx", "other")
	eve, eveSess := verifiedUser(t, fx.s, keyB, other.ID, "eve@example.com")
	wantStatus(t, call(t, fx.s, "PUT", "/api/v1/nodes/"+fileB.ID+"/permissions/"+eve.ID, keyB,
		map[string]any{"role": "editor"}), 200)
	tokB := decode[officeJSON](t, callSession(t, fx.s, eveSess, "POST", "/api/v1/nodes/"+fileB.ID+"/office",
		map[string]any{"mode": "edit"})).Token

	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tokB, nil, wopiLockHdr("LOCK", "X")), 404)
	tokA := fx.openToken(t, "edit")
	rec := fx.wopiCall(t, "POST", nodeRoute(fx.doc.ID), tokA, nil, wopiLockHdr("GET_LOCK", ""))
	if v := rec.Result().Header["X-Wopi-Lock"]; len(v) != 1 || v[0] != "" {
		t.Errorf("a token of another account locked the file: GetLock %v", v)
	}
	// The other account's file is unaffected by this account's lock on its own file.
	fx.lockFor(t, tokA, "A1")
	wantStatus(t, fx.wopiCall(t, "POST", nodeRoute(fileB.ID), tokB, nil, wopiLockHdr("LOCK", "B1")), 200)
}

// TestWOPIRelockAfterExpiryAuditsTheExpiry: an editor whose lock expired takes a new one. The expired row is replaced, and
// its expiry is audited at the time of the new lock, since the sweep has not yet recorded it.
func TestWOPIRelockAfterExpiryAuditsTheExpiry(t *testing.T) {
	t.Parallel()
	fx := newOfficeFixture(t)
	clock := fx.useLockClock(t)
	tok := fx.openToken(t, "edit")
	docRoute := nodeRoute(fx.doc.ID)
	fx.lockFor(t, tok, "R1")
	clock.add(31 * time.Minute)
	wantStatus(t, fx.wopiCall(t, "POST", docRoute, tok, nil, wopiLockHdr("LOCK", "R2")), 200)
	wantStatus(t, fx.wopiCall(t, "POST", docRoute+"/contents", tok, strings.NewReader("after"), saveHdr("R2")), 200)
	if got := fx.fileText(t, fx.doc.ID); got != "after" {
		t.Errorf("the save under the new lock left %q", got)
	}
	entries := decode[listResponse[auditJSON]](t, call(t, fx.s, "GET", "/api/v1/accounts/"+fx.acc.ID+"/audit", fx.key, nil))
	expired := 0
	for _, a := range entries.Items {
		if a.Action == "node.unlock" && a.Target == fx.doc.ID {
			expired++
		}
	}
	if expired != 1 {
		t.Errorf("node.unlock entries for the file = %d, want 1 (the expiry, recorded when the new lock replaced it)", expired)
	}
}
