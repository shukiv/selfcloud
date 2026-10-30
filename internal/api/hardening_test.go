// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestPasswordFailsPrune checks that counts of sessions that ended for other reasons do not stay
// in memory: once stale, a count is swept, and a stale count restarts at one.
func TestPasswordFailsPrune(t *testing.T) {
	t.Parallel()
	var f passwordFails
	a, b := []byte("session-a"), []byte("session-b")
	f.add(a)
	f.add(b)

	// Age both entries past the window, and force a sweep on the next call.
	f.mu.Lock()
	for k, v := range f.n {
		v.last = v.last.Add(-2 * passwordFailWindow)
		f.n[k] = v
	}
	f.lastSweep = time.Time{}
	f.mu.Unlock()

	if n := f.add(a); n != 1 {
		t.Errorf("stale count restarted at %d, want 1", n)
	}
	if _, ok := f.n[string(b)]; ok {
		t.Error("stale entry for the other session was not swept")
	}
}

// TestMailBudgets checks the limits on verification and reset mail: 30 an hour per account, and 3
// an hour to one address. A refused request creates nothing.
func TestMailBudgets(t *testing.T) {
	t.Parallel()
	rec := &mail.Recorder{}
	s := newAuthServer(t, rec)
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	users := "/api/v1/accounts/" + acc.ID + "/users"

	// Per address: one create and two resends are mail; the next resend is refused.
	ann := decode[userResponse](t, call(t, s, "POST", users, key,
		map[string]any{"email": "ann@example.com", "verification": "email"}))
	wantStatus(t, call(t, s, "POST", users+"/"+ann.ID+"/verification", key, map[string]any{"method": "email"}), 200)
	wantStatus(t, call(t, s, "POST", users+"/"+ann.ID+"/verification", key, map[string]any{"method": "email"}), 200)
	wantError(t, call(t, s, "POST", users+"/"+ann.ID+"/verification", key, map[string]any{"method": "email"}),
		429, apierr.RateLimited)
	// A refused send does not change the user's verification: still pending, with the earlier token.
	if got := decode[userJSON](t, call(t, s, "GET", users+"/"+ann.ID, key, nil)); got.Status != store.UserPending {
		t.Errorf("status after refusal = %s", got.Status)
	}

	// Per account: a second account, so the first one's spent budget does not count. Thirty
	// messages to distinct addresses, then the 31st is refused.
	acc2, key2 := newAccount(t, s, admin, "Beta")
	users2 := "/api/v1/accounts/" + acc2.ID + "/users"
	for i := 0; i < 30; i++ {
		wantStatus(t, call(t, s, "POST", users2, key2,
			map[string]any{"email": "u" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@example.com",
				"verification": "email"}), 201)
	}
	wantError(t, call(t, s, "POST", users2, key2,
		map[string]any{"email": "late@example.com", "verification": "email"}), 429, apierr.RateLimited)
	// Links are not mail, so the account budget does not stop them.
	wantStatus(t, call(t, s, "POST", users2, key2,
		map[string]any{"email": "link@example.com", "verification": "link"}), 201)

	// A reset mail to an active user counts against the same address limit.
	active, _ := verifiedUser(t, s, key, acc.ID, "active@example.com")
	wantStatus(t, call(t, s, "POST", users+"/"+active.ID+"/password-reset", key, map[string]any{"method": "email"}), 200)
	wantStatus(t, call(t, s, "POST", users+"/"+active.ID+"/password-reset", key, map[string]any{"method": "email"}), 200)
	wantStatus(t, call(t, s, "POST", users+"/"+active.ID+"/password-reset", key, map[string]any{"method": "email"}), 200)
	wantError(t, call(t, s, "POST", users+"/"+active.ID+"/password-reset", key, map[string]any{"method": "email"}),
		429, apierr.RateLimited)
}

// TestDisableBeatsConcurrentSignIn runs sign-ins and a disable at the same time. Once the disable
// has answered, no session made by a sign-in may still work: the sign-in re-reads the user under
// the write lock, so a sign-in that was checked before the disable cannot mint a live session.
func TestDisableBeatsConcurrentSignIn(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, _ := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	const n = 16
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i] = login(t, s, "ann@example.com", testPassword, "")
		}(i)
	}
	var disabled *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		disabled = call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+ann.ID, key,
			map[string]any{"status": "disabled"})
	}()
	wg.Wait()
	wantStatus(t, disabled, 200)

	for i, rec := range recs {
		if rec.Code != http.StatusOK {
			continue
		}
		sess := sessionFrom(t, s, rec)
		if got := callSession(t, s, testSession{cookie: sess.cookie}, "GET", "/api/v1/me", nil); got.Code == 200 {
			t.Errorf("sign-in %d left a live session after the disable", i)
		}
	}
}

// TestPasswordChangeBeatsConcurrentDisable does the same for a password change: a change checked
// before a disable must not mint a session for the disabled user.
func TestPasswordChangeBeatsConcurrentDisable(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	ann, sess := verifiedUser(t, s, key, acc.ID, "ann@example.com")

	var wg sync.WaitGroup
	changed := make(chan *httptest.ResponseRecorder, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		changed <- callSession(t, s, sess, "POST", "/api/v1/me/password", map[string]any{
			"current_password": testPassword, "new_password": "a new passphrase"})
	}()
	go func() {
		defer wg.Done()
		call(t, s, "PATCH", "/api/v1/accounts/"+acc.ID+"/users/"+ann.ID, key, map[string]any{"status": "disabled"})
	}()
	wg.Wait()
	rec := <-changed
	if rec.Code == http.StatusNoContent {
		fresh := sessionFrom204(t, s, rec)
		if got := callSession(t, s, testSession{cookie: fresh.cookie}, "GET", "/api/v1/me", nil); got.Code == 200 {
			t.Error("a password change left a live session after the disable")
		}
	}
}

// TestLastAdminIgnoresExpiringKeys checks the last-admin guard: a key with an expiry does not keep
// the installation reachable, so it does not count.
func TestLastAdminIgnoresExpiringKeys(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	root, sess := newAdminUser(t, s, admin, "root@example.com")
	ctx := t.Context()

	// Revoke the bootstrap key and add one that expires in an hour.
	_, prefix, hash := auth.NewKey(auth.AdminKeyPrefix)
	exp := time.Now().Add(time.Hour)
	err := s.store.Write(ctx, func(tx *store.Tx) error {
		k, err := s.store.APIKeyByHash(ctx, auth.HashKey(admin))
		if err != nil {
			return err
		}
		if err := tx.RevokeAPIKey(ctx, "", k.ID, store.Now()); err != nil {
			return err
		}
		return tx.InsertAPIKey(ctx, &store.APIKey{ID: "key_expiring", Name: "soon", Prefix: prefix, Hash: hash,
			CreatedAt: store.Now(), ExpiresAt: &exp})
	})
	if err != nil {
		t.Fatal(err)
	}
	// The admin user is the only admin, and the only key expires: it may not be deleted.
	refused := wantError(t, callSession(t, s, sess, "DELETE", "/api/v1/admins/"+root.ID, nil), 409, apierr.Conflict)
	if refused.Details["reason"] != "last_admin" {
		t.Errorf("reason = %v, want last_admin", refused.Details["reason"])
	}
}

// TestRoleChangeEndsGrants checks that leaving the member role ends a user's grants, so a return to
// member does not restore them, and that the removal is audited.
func TestRoleChangeEndsGrants(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Reports")
	ann, _ := verifiedUser(t, s, key, acc.ID, "ann@example.com")
	userPath := "/api/v1/accounts/" + acc.ID + "/users/" + ann.ID
	grantsPath := "/api/v1/accounts/" + acc.ID + "/users/" + ann.ID + "/grants"
	wantStatus(t, call(t, s, "PUT", "/api/v1/accounts/"+acc.ID+"/folders/"+f.ID+"/grants/"+ann.ID, key,
		map[string]any{"level": "write"}), 200)

	// A second active owner, so that ann may leave the owner role: the last active owner may not.
	makeOwner(t, s, key, acc.ID, "boss@example.com")
	wantStatus(t, call(t, s, "PATCH", userPath, key, map[string]any{"role": "owner"}), 200)
	if got := decode[listResponse[grantJSON]](t, call(t, s, "GET", grantsPath, key, nil)); len(got.Items) != 0 {
		t.Fatalf("grants after promotion = %+v", got.Items)
	}
	wantStatus(t, call(t, s, "PATCH", userPath, key, map[string]any{"role": "member"}), 200)
	if got := decode[listResponse[grantJSON]](t, call(t, s, "GET", grantsPath, key, nil)); len(got.Items) != 0 {
		t.Fatalf("old grants came back after returning to member: %+v", got.Items)
	}

	entries := decode[listResponse[auditJSON]](t, call(t, s, "GET", "/api/v1/accounts/"+acc.ID+"/audit", admin, nil))
	found := false
	for _, e := range entries.Items {
		if e.Action == "grant.remove" && e.Target == ann.ID {
			found = true
		}
	}
	if !found {
		t.Error("the removal on role change is not audited")
	}
}
