// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/config"
)

// searchAs runs GET /api/v1/directory as the given credential (a key or a token) with the query.
func searchAs(t *testing.T, fx *sharingFixture, cred, q, kind string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/directory?q=" + url.QueryEscape(q)
	if kind != "" {
		path += "&kind=" + kind
	}
	return call(t, fx.s, "GET", path, cred, nil)
}

// searchSession runs the same search as a signed-in user.
func searchSession(t *testing.T, fx *sharingFixture, sess testSession, q string) *httptest.ResponseRecorder {
	t.Helper()
	return callSession(t, fx.s, sess, "GET", "/api/v1/directory?q="+url.QueryEscape(q), nil)
}

// namesIn lists the names of the entries in a directory answer.
func namesIn(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	wantStatus(t, rec, 200)
	var out []string
	for _, e := range decode[listResponse[directoryJSON]](t, rec).Items {
		out = append(out, e.Name)
	}
	return out
}

// TestDirectoryFindsActiveColleague: a member finds another active user of its account by its email
// local part, and gets its ID, kind, name and email, and nothing else about it.
func TestDirectoryFindsActiveColleague(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	rec := searchSession(t, fx, fx.annSess, "bob")
	wantStatus(t, rec, 200)
	items := decode[listResponse[directoryJSON]](t, rec).Items
	if len(items) != 1 || items[0].ID != fx.bob.ID || items[0].Kind != "user" || items[0].Email != "bob@example.com" {
		t.Fatalf("search bob = %+v, want bob as a user", items)
	}
	for _, field := range []string{`"status"`, `"role"`, `"locale"`, `"password`, `"external_id"`} {
		if strings.Contains(rec.Body.String(), field) {
			t.Errorf("directory answer carries %s: %s", field, rec.Body)
		}
	}
}

// TestDirectoryHidesPendingUsers: a user that has not verified is not listed, whatever its name.
func TestDirectoryHidesPendingUsers(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.pendingUser(t, "pat@example.com")
	if got := namesIn(t, searchSession(t, fx, fx.annSess, "pat")); len(got) != 0 {
		t.Errorf("a pending user was found: %v", got)
	}
	// A disabled user is not active either.
	dis := fx.activeUser(t, "dan@example.com", "Dan Disabled", "en")
	wantStatus(t, call(t, fx.s, "PATCH", "/api/v1/accounts/"+fx.acc.ID+"/users/"+dis.ID, fx.key,
		map[string]any{"status": "disabled"}), 200)
	if got := namesIn(t, searchSession(t, fx, fx.annSess, "dan")); len(got) != 0 {
		t.Errorf("a disabled user was found: %v", got)
	}
}

// TestDirectoryHidesOtherAccounts: a user of another account is never listed.
func TestDirectoryHidesOtherAccounts(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	other, _ := newAccount(t, fx.s, fx.admin, "Other Co")
	rec := call(t, fx.s, "POST", "/api/v1/accounts/"+other.ID+"/users", fx.admin,
		map[string]any{"email": "carol@example.com", "display_name": "Carol Other", "verification": "none"})
	wantStatus(t, rec, 201)
	if got := namesIn(t, searchSession(t, fx, fx.annSess, "carol")); len(got) != 0 {
		t.Errorf("a user of another account was found: %v", got)
	}
	if got := namesIn(t, searchSession(t, fx, fx.annSess, "other co")); len(got) != 0 {
		t.Errorf("an account's name matched a user search: %v", got)
	}
}

// TestDirectoryFindsGroups: a group is found by part of its name, case-insensitively, and only when the
// kind asks for groups or for all.
func TestDirectoryFindsGroups(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	gid := fx.group(t, "Finance Team")
	rec := searchSession(t, fx, fx.annSess, "FINANCE")
	items := decode[listResponse[directoryJSON]](t, rec).Items
	if len(items) != 1 || items[0].ID != gid || items[0].Kind != "group" || items[0].Email != "" {
		t.Fatalf("search FINANCE = %+v, want the group", items)
	}
	userOnly := callSession(t, fx.s, fx.annSess, "GET", "/api/v1/directory?q=finance&kind=user", nil)
	if got := namesIn(t, userOnly); len(got) != 0 {
		t.Errorf("kind=user listed a group: %v", got)
	}
}

// TestDirectoryQueryBounds: q is trimmed, then must be 2 to 100 characters.
func TestDirectoryQueryBounds(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for _, q := range []string{"", " ", "a", " a ", strings.Repeat("a", 101)} {
		wantError(t, searchAs(t, fx, fx.key, q, ""), 400, apierr.InvalidRequest)
	}
	wantStatus(t, searchAs(t, fx, fx.key, "ab", ""), 200)
	wantStatus(t, searchAs(t, fx, fx.key, strings.Repeat("a", 100), ""), 200)
	wantStatus(t, searchAs(t, fx, fx.key, "  ab  ", ""), 200)
	wantError(t, searchAs(t, fx, fx.key, "ab", "people"), 400, apierr.InvalidRequest)
}

// TestDirectoryTakesWildcardsLiterally: % and _ in q match only themselves.
func TestDirectoryTakesWildcardsLiterally(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.activeUser(t, "a_b@example.com", "Ann Underscore", "en")
	// "an" is a real prefix of the fixture's ann: a control that the search works at all.
	if got := namesIn(t, searchAs(t, fx, fx.key, "an", "user")); len(got) == 0 {
		t.Fatal("control search for an found nothing")
	}
	// Two users that differ only where a wildcard would reach: "p%" must find p%q and not pxq.
	fx.activeUser(t, "p%q@example.com", "Pat Percent", "en")
	fx.activeUser(t, "pxq@example.com", "Pat Letter", "en")
	if got := namesIn(t, searchAs(t, fx, fx.key, "p%", "user")); len(got) != 1 || got[0] != "Pat Percent" {
		t.Errorf("literal p%% found %v, want only Pat Percent", got)
	}
	// Neither wildcard may act as a wildcard: "a%" would match every local part starting with a,
	// and "_n" would match "ann" with any first character.
	for _, q := range []string{"a%", "_n", "%an", "a__", "p_"} {
		if got := namesIn(t, searchAs(t, fx, fx.key, q, "user")); len(got) != 0 {
			t.Errorf("q %q matched %v; wildcards must be literal", q, got)
		}
	}
	// A literal underscore in a local part is found by a literal query: "a_" is a real prefix of a_b.
	if got := namesIn(t, searchAs(t, fx, fx.key, "a_", "user")); len(got) != 1 {
		t.Errorf("literal a_ found %v, want the one user", got)
	}
}

// TestDirectoryAnswerIsCapped: an answer holds at most 20 entries, users and groups together.
func TestDirectoryAnswerIsCapped(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	for i := 0; i < 25; i++ {
		fx.activeUser(t, fmt.Sprintf("zed%02d@example.com", i), "", "en")
	}
	for i := 0; i < 3; i++ {
		fx.group(t, fmt.Sprintf("Zed group %d", i))
	}
	rec := searchAs(t, fx, fx.key, "zed", "")
	wantStatus(t, rec, 200)
	if n := len(decode[listResponse[directoryJSON]](t, rec).Items); n != 20 {
		t.Errorf("answer holds %d entries, want 20", n)
	}
}

// TestDirectoryRefusesAdminsAndTokens: an installation admin gets forbidden, and so does every delegated
// token, whether it names a user or not: a token is narrowed to its folders and never lists the account's
// people. A session user and the account key are allowed.
func TestDirectoryRefusesAdminsAndTokens(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	wantError(t, searchAs(t, fx, fx.admin, "bob", ""), 403, apierr.Forbidden)
	// An installation admin signed in as a user is refused as well: its kind is user, so only the admin check stops it.
	_, adminSess := newAdminUser(t, fx.s, fx.admin, "root@example.com")
	wantError(t, searchSession(t, fx, adminSess, "bob"), 403, apierr.Forbidden)

	keyMode := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"folders": []string{fx.fld.ID}})
	wantError(t, searchAs(t, fx, keyMode, "bob", ""), 403, apierr.Forbidden)

	wantStatus(t, searchAs(t, fx, fx.key, "bob", ""), 200)
	wantStatus(t, searchSession(t, fx, fx.annSess, "bob"), 200)
	userTok := mintFor(t, fx.s, fx.key, fx.acc.ID, map[string]any{"user": fx.ann.ID, "folders": []string{fx.fld.ID}})
	wantError(t, searchAs(t, fx, userTok, "bob", ""), 403, apierr.Forbidden)
}

// TestDirectoryIsRateLimitedPerCaller: a caller that searches past its allowance is refused, and the answer
// says so.
func TestDirectoryIsRateLimitedPerCaller(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 80; i++ {
		last = searchSession(t, fx, fx.annSess, "bob")
		if last.Code == http.StatusTooManyRequests {
			return
		}
	}
	t.Fatalf("80 searches were never refused; last status %d", last.Code)
}

// TestInfoReportsSharingMaxExpiryDays: the cap a share may run is in /info in whole days, rounded up, and
// 0 means no cap, so a cap shorter than a day is never read as no cap.
func TestInfoReportsSharingMaxExpiryDays(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	days := func(d time.Duration) float64 {
		t.Helper()
		s.cfg.Sharing.MaxExpiry = config.Duration(d)
		rec := call(t, s, "GET", "/api/v1/info", "", nil)
		wantStatus(t, rec, 200)
		v, ok := decode[map[string]any](t, rec)["sharing_max_expiry_days"].(float64)
		if !ok {
			t.Fatalf("/info has no sharing_max_expiry_days: %s", rec.Body)
		}
		return v
	}
	cases := []struct {
		d    time.Duration
		want float64
	}{
		{0, 0},
		{time.Hour, 1},
		{24 * time.Hour, 1},
		{36 * time.Hour, 2},
		{365 * 24 * time.Hour, 365},
	}
	for _, c := range cases {
		if got := days(c.d); got != c.want {
			t.Errorf("max_expiry %v: sharing_max_expiry_days = %v, want %v", c.d, got, c.want)
		}
	}
}
