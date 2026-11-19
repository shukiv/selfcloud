// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/search"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestSearchOrderParam: order is empty or newest; anything else is refused. The order alone is not a filter, and newest
// belongs to a name search with no terms: a query or in=content with it is refused.
func TestSearchOrderParam(t *testing.T) {
	t.Parallel()
	fx := newSearchFixture(t)
	for _, c := range []struct{ query, message string }{
		{"q=report&order=oldest", "order must be newest"},
		{"q=report&order=NEWEST", "order must be newest"},
		{"order=newest", "q or a filter is required"},
		{"order=newest&q=report", "order=newest needs no q"},
		{"order=newest&q=report+2026", "order=newest needs no q"},
		{"order=newest&in=content&q=report", "order=newest needs no q"},
	} {
		rec := fx.search(t, c.query)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), c.message) {
			t.Errorf("%s: %d %s; want 400 naming %q", c.query, rec.Code, rec.Body, c.message)
		}
		wantError(t, rec, 400, apierr.InvalidRequest)
	}
	wantStatus(t, fx.search(t, "order=newest&kind=file"), 200)
	wantStatus(t, fx.search(t, "q=report&order="), 200)
}

// newestFixture is an account with five files whose created times the test sets exactly: one newest, three tied in the
// middle (created in the order tie-1, tie-2, tie-3, so their rowids run that way), and one oldest.
type newestFixture struct {
	s    *Server
	key  string
	acc  accountJSON
	fld  folderJSON
	sess testSession
}

func newNewestFixture(t *testing.T) *newestFixture {
	t.Helper()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	fx := &newestFixture{s: s, key: key, acc: acc}
	fx.fld = newFolder(t, s, key, acc.ID, "Docs")
	_, fx.sess = ownerUser(t, s, key, acc.ID, "owner@example.com")
	for _, c := range []struct {
		name string
		at   int64
	}{
		{"solo-old.txt", 1000},
		{"tie-1.txt", 3000},
		{"tie-2.txt", 3000},
		{"tie-3.txt", 3000},
		{"solo-new.txt", 5000},
	} {
		n := putTyped(t, s, key, fx.fld.ID, c.name, "text/plain", "x")
		setCreatedAt(t, s, n.ID, c.at)
	}
	return fx
}

// newestOrder is the five files newest first, the three tied created times broken by rowid descending.
var newestOrder = []string{"solo-new.txt", "tie-3.txt", "tie-2.txt", "tie-1.txt", "solo-old.txt"}

// TestSearchNewestOrdersByCreated: order=newest reads the nodes by created time descending, and equal created times by
// rowid descending, so the later-created of two tied nodes comes first.
func TestSearchNewestOrdersByCreated(t *testing.T) {
	t.Parallel()
	fx := newNewestFixture(t)
	rec := callSession(t, fx.s, fx.sess, "GET", "/api/v1/search?order=newest&kind=file&limit=50", nil)
	wantStatus(t, rec, 200)
	got := decode[testPage](t, rec).Items
	names := make([]string, len(got))
	for i, h := range got {
		names[i] = h.Name
	}
	if strings.Join(names, "|") != strings.Join(newestOrder, "|") {
		t.Fatalf("newest order = %q, want %q", names, newestOrder)
	}
}

// TestSearchNewestPagesEveryItemOnce: pages of two over the five files return every file once, in the newest order, with
// no repeat and no gap. The three tied created times straddle the first page's edge: one on the first page, two on the
// second, and the cursor carries both the created time and the rowid.
func TestSearchNewestPagesEveryItemOnce(t *testing.T) {
	t.Parallel()
	fx := newNewestFixture(t)
	var got []string
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		path := "/api/v1/search?order=newest&kind=file&limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		p := decode[testPage](t, callSession(t, fx.s, fx.sess, "GET", path, nil))
		for _, h := range p.Items {
			if seen[h.Name] {
				t.Fatalf("%s returned twice", h.Name)
			}
			seen[h.Name] = true
			got = append(got, h.Name)
		}
		if cursor = p.NextCursor; cursor == "" {
			break
		}
	}
	if strings.Join(got, "|") != strings.Join(newestOrder, "|") {
		t.Fatalf("paged walk = %q, want %q", got, newestOrder)
	}
}

// TestSearchNewestCursorIsBoundToItsOrderAndFilters: a cursor made for newest does not open for the default order, and a
// default-order cursor does not open for newest; a changed created bound refuses the newest cursor too. The same request
// still opens it.
func TestSearchNewestCursorIsBoundToItsOrderAndFilters(t *testing.T) {
	t.Parallel()
	fx := newNewestFixture(t)
	first := decode[testPage](t, callSession(t, fx.s, fx.sess, "GET", "/api/v1/search?order=newest&kind=file&limit=2", nil))
	if first.NextCursor == "" {
		t.Fatal("the first newest page has no cursor")
	}
	cursor := url.QueryEscape(first.NextCursor)
	wantError(t, callSession(t, fx.s, fx.sess, "GET", "/api/v1/search?kind=file&limit=2&cursor="+cursor, nil),
		400, apierr.InvalidRequest)
	wantError(t, callSession(t, fx.s, fx.sess, "GET",
		"/api/v1/search?order=newest&kind=file&created_after=1&limit=2&cursor="+cursor, nil), 400, apierr.InvalidRequest)
	wantStatus(t, callSession(t, fx.s, fx.sess, "GET", "/api/v1/search?order=newest&kind=file&limit=2&cursor="+cursor, nil), 200)

	plain := decode[testPage](t, callSession(t, fx.s, fx.sess, "GET", "/api/v1/search?kind=file&limit=2", nil))
	if plain.NextCursor == "" {
		t.Fatal("the first default-order page has no cursor")
	}
	wantError(t, callSession(t, fx.s, fx.sess, "GET",
		"/api/v1/search?order=newest&kind=file&limit=2&cursor="+url.QueryEscape(plain.NextCursor), nil),
		400, apierr.InvalidRequest)
}

// TestNewestQueryPlanUsesTheIndex: the newest candidate read seeks the created index, for a zero and a non-zero position
// alike, and never sorts: a sort would read the whole account on every page. With a position, the created bound is part
// of the seek. Mutation: an ORDER BY the index does not give, or the bound moved off the index, fails here.
func TestNewestQueryPlanUsesTheIndex(t *testing.T) {
	t.Parallel()
	fx := newNewestFixture(t)
	for _, pos := range []search.Pos{{}, {A: 3000, B: 42}} {
		req := &search.Request{AccountID: fx.acc.ID, Order: search.OrderNewest, ModifiedFrom: -1, ModifiedTo: -1,
			SizeMin: -1, SizeMax: -1, Limit: 25}
		query, args, err := req.NameSQL(pos, 100)
		if err != nil {
			t.Fatal(err)
		}
		err = fx.s.store.Read(context.Background(), func(q store.Querier) error {
			rows, err := q.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					return err
				}
				plan = append(plan, detail)
			}
			t.Logf("newest plan at %+v: %s", pos, strings.Join(plan, " | "))
			if len(plan) == 0 || !strings.Contains(plan[0], "SEARCH") || !strings.Contains(plan[0], "nodes_created") {
				t.Errorf("position %+v: the created index is not the outer seek: %v", pos, plan)
			}
			for _, d := range plan {
				if strings.Contains(d, "TEMP B-TREE") {
					t.Errorf("position %+v: the newest query sorts the account: %s", pos, d)
				}
			}
			if pos.A != 0 {
				bound := false
				for _, d := range plan {
					if strings.Contains(d, "created_at") {
						bound = true
					}
				}
				if !bound {
					t.Errorf("position %+v: the seek carries no created bound: %v", pos, plan)
				}
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
