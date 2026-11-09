// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// accountNames lists the names of one page of GET /accounts with query, and the cursor that follows it.
func accountNames(t *testing.T, s *Server, admin, query string) ([]string, string) {
	t.Helper()
	page := decode[listResponse[accountJSON]](t, call(t, s, "GET", "/api/v1/accounts"+query, admin, nil))
	var names []string
	for _, a := range page.Items {
		names = append(names, a.Name)
	}
	return names, page.NextCursor
}

// TestListAccountsSortedWalk checks ?sort=name and ?sort=created with ?order=, walked a page at a time: each walk visits every
// account once in its order, and a sorted page carries the usage fields the tenants list shows.
func TestListAccountsSortedWalk(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, n := range []string{"gamma", "Alpha", "beta"} {
		wantStatus(t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": n}), 201)
		// Accounts made in one millisecond tie on creation time, and a tie breaks on the random ID: the order of the created
		// walk is then not the order of the test, so each account is made a few milliseconds after the last.
		time.Sleep(3 * time.Millisecond)
	}
	walk := func(sortQuery string) []string {
		t.Helper()
		var got []string
		cursor := ""
		for range 10 {
			q := "?limit=1" + sortQuery
			if cursor != "" {
				q += "&cursor=" + url.QueryEscape(cursor)
			}
			names, next := accountNames(t, s, admin, q)
			got = append(got, names...)
			if next == "" {
				return got
			}
			cursor = next
		}
		t.Fatal("the walk did not end")
		return nil
	}
	cases := []struct {
		query string
		want  []string
	}{
		{"&sort=name", []string{"Alpha", "beta", "gamma"}},
		{"&sort=name&order=asc", []string{"Alpha", "beta", "gamma"}},
		{"&sort=name&order=desc", []string{"gamma", "beta", "Alpha"}},
		{"&sort=created", []string{"gamma", "Alpha", "beta"}},
		{"&sort=created&order=desc", []string{"beta", "Alpha", "gamma"}},
	}
	for _, c := range cases {
		if got := walk(c.query); !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.query, got, c.want)
		}
	}

	rec := call(t, s, "GET", "/api/v1/accounts?sort=name", admin, nil)
	wantStatus(t, rec, 200)
	for _, key := range []string{`"used_bytes"`, `"quota_bytes"`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("sorted accounts list has no %s", key)
		}
	}
}

// TestListAccountsSortCursor checks that a sorted cursor opens only for the same sort, and that a cursor of the default walk
// is refused by a sorted listing and the other way round. Each refusal is invalid_request.
func TestListAccountsSortCursor(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, n := range []string{"gamma", "Alpha", "beta"} {
		wantStatus(t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": n}), 201)
		// Accounts made in one millisecond tie on creation time, and a tie breaks on the random ID: the order of the created
		// walk is then not the order of the test, so each account is made a few milliseconds after the last.
		time.Sleep(3 * time.Millisecond)
	}
	_, byName := accountNames(t, s, admin, "?limit=1&sort=name")
	_, byCreated := accountNames(t, s, admin, "?limit=1&sort=created")
	_, plain := accountNames(t, s, admin, "?limit=1")
	if byName == "" || byCreated == "" || plain == "" {
		t.Fatalf("expected a cursor from each walk: name %q, created %q, plain %q", byName, byCreated, plain)
	}
	refused := []string{
		"?limit=1&sort=created&cursor=" + url.QueryEscape(byName),
		"?limit=1&sort=name&order=desc&cursor=" + url.QueryEscape(byName),
		"?limit=1&sort=created&order=desc&cursor=" + url.QueryEscape(byCreated),
		"?limit=1&sort=name&cursor=" + url.QueryEscape(plain),
		"?limit=1&cursor=" + url.QueryEscape(byName),
		"?limit=1&cursor=" + url.QueryEscape(plain) + "&sort=created",
	}
	for _, q := range refused {
		wantError(t, call(t, s, "GET", "/api/v1/accounts"+q, admin, nil), 400, apierr.InvalidRequest)
	}
	// The cursor opens for its own sort.
	if names, _ := accountNames(t, s, admin, "?limit=1&sort=name&cursor="+url.QueryEscape(byName)); len(names) != 1 || names[0] != "beta" {
		t.Errorf("name cursor resumed at %v, want [beta]", names)
	}
}

// TestListAccountsSortValues refuses a sort key, an order and a repeated parameter that are not allowed.
func TestListAccountsSortValues(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, q := range []string{"?sort=usage", "?sort=name&order=up", "?sort=name&sort=created", "?order=desc&order=asc"} {
		wantError(t, call(t, s, "GET", "/api/v1/accounts"+q, admin, nil), 400, apierr.InvalidRequest)
	}
}
