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

// sortNames are the accounts the sort tests make, in this order. Byte order (Banana, apple, cherry) and folded order (apple,
// Banana, cherry) differ, so a name sort that loses the name collation fails here; the creation order is a third order.
var sortNames = []string{"cherry", "Banana", "apple"}

// TestListAccountsSortedWalk checks ?sort=name and ?sort=created with ?order=, walked a page at a time: each walk visits every
// account once in its order, and a sorted page carries the usage fields the tenants list shows.
func TestListAccountsSortedWalk(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, n := range sortNames {
		wantStatus(t, call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": n, "quota_bytes": 5000}), 201)
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
		{"&sort=name", []string{"apple", "Banana", "cherry"}},
		{"&sort=name&order=asc", []string{"apple", "Banana", "cherry"}},
		{"&sort=name&order=desc", []string{"cherry", "Banana", "apple"}},
		{"&sort=created", []string{"cherry", "Banana", "apple"}},
		{"&sort=created&order=desc", []string{"apple", "Banana", "cherry"}},
	}
	for _, c := range cases {
		if got := walk(c.query); !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.query, got, c.want)
		}
	}

	// A sorted page carries each account's usage and limit with their values, not only their names: the tenants list reads
	// them, and makes no request per tenant. An upload to apple makes its used bytes more than zero.
	apple := ""
	for _, a := range decode[listResponse[accountJSON]](t, call(t, s, "GET", "/api/v1/accounts?sort=name", admin, nil)).Items {
		if a.Name == "apple" {
			apple = a.ID
		}
	}
	rec := call(t, s, "POST", "/api/v1/accounts/"+apple+"/keys", admin, map[string]any{"name": "test"})
	wantStatus(t, rec, 201)
	key := decode[createdKey](t, rec).Key
	f := newFolder(t, s, key, apple, "Docs")
	wantStatus(t, writeFile(t, s, key, f.ID, "a.txt", "four"), 201)
	page := decode[listResponse[accountJSON]](t, call(t, s, "GET", "/api/v1/accounts?sort=name", admin, nil))
	if len(page.Items) != 3 {
		t.Fatalf("sorted page has %d accounts, want 3", len(page.Items))
	}
	for _, a := range page.Items {
		wantUsed := int64(0)
		if a.Name == "apple" {
			wantUsed = 4
		}
		if a.QuotaBytes != 5000 || a.UsedBytes != wantUsed {
			t.Errorf("%s: quota_bytes %d used_bytes %d, want 5000 and %d", a.Name, a.QuotaBytes, a.UsedBytes, wantUsed)
		}
	}
}

// TestListAccountsSortCursor checks that a sorted cursor opens only for the same sort, and that a cursor of the default walk
// is refused by a sorted listing and the other way round. Each refusal is invalid_request.
func TestListAccountsSortCursor(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, n := range sortNames {
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
	if names, _ := accountNames(t, s, admin, "?limit=1&sort=name&cursor="+url.QueryEscape(byName)); len(names) != 1 || names[0] != "Banana" {
		t.Errorf("name cursor resumed at %v, want [Banana]", names)
	}
}

// TestListAccountsSortCursorFilter checks that a sorted cursor is bound to the walk's filters as well as its sort: a cursor
// from one status, search or external ID resumes no walk of another, and is refused as invalid_request.
func TestListAccountsSortCursorFilter(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	for _, n := range []string{"amber one", "amber two", "birch one", "birch two"} {
		rec := call(t, s, "POST", "/api/v1/accounts", admin, map[string]any{"name": n, "external_id": "ext-" + strings.ReplaceAll(n, " ", "-")})
		wantStatus(t, rec, 201)
		if strings.HasSuffix(n, "two") {
			wantStatus(t, call(t, s, "PATCH", "/api/v1/accounts/"+decode[accountJSON](t, rec).ID, admin, map[string]any{"status": "suspended"}), 200)
		}
	}
	_, active := accountNames(t, s, admin, "?limit=1&sort=name&status=active")
	_, amber := accountNames(t, s, admin, "?limit=1&sort=name&q=amber")
	_, all := accountNames(t, s, admin, "?limit=1&sort=name")
	if active == "" || amber == "" || all == "" {
		t.Fatalf("expected a cursor from each walk: active %q, amber %q, all %q", active, amber, all)
	}
	refused := []string{
		"?limit=1&sort=name&status=suspended&cursor=" + url.QueryEscape(active),
		"?limit=1&sort=name&cursor=" + url.QueryEscape(active),
		"?limit=1&sort=name&q=birch&cursor=" + url.QueryEscape(amber),
		"?limit=1&sort=name&q=amber&status=active&cursor=" + url.QueryEscape(amber),
		"?limit=1&sort=name&external_id=ext-amber-one&cursor=" + url.QueryEscape(all),
		"?limit=1&sort=name&status=active&cursor=" + url.QueryEscape(all),
	}
	for _, q := range refused {
		wantError(t, call(t, s, "GET", "/api/v1/accounts"+q, admin, nil), 400, apierr.InvalidRequest)
	}
	// Each cursor still opens for its own walk.
	if names, _ := accountNames(t, s, admin, "?limit=1&sort=name&status=active&cursor="+url.QueryEscape(active)); len(names) != 1 || names[0] != "birch one" {
		t.Errorf("active cursor resumed at %v, want [birch one]", names)
	}
	if names, _ := accountNames(t, s, admin, "?limit=1&sort=name&q=amber&cursor="+url.QueryEscape(amber)); len(names) != 1 || names[0] != "amber two" {
		t.Errorf("amber cursor resumed at %v, want [amber two]", names)
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
