// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/auth"
	"github.com/shukiv/selfcloud/internal/store"
)

// countingQuerier is a store querier that counts the statements it runs and keeps their text, so a test can see how many
// queries a place lookup makes and what each one asks for. It passes every statement to the querier it wraps.
type countingQuerier struct {
	q       store.Querier
	queries []string
}

func (c *countingQuerier) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	c.queries = append(c.queries, query)
	return c.q.ExecContext(ctx, query, args...)
}

func (c *countingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.queries = append(c.queries, query)
	return c.q.QueryContext(ctx, query, args...)
}

func (c *countingQuerier) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.queries = append(c.queries, query)
	return c.q.QueryRowContext(ctx, query, args...)
}

// reachSeeds returns how many node IDs the batched reach query was given: its seed list has one "(?)" per ID.
func reachSeeds(t *testing.T, queries []string) int {
	t.Helper()
	for _, q := range queries {
		if strings.Contains(q, "WITH RECURSIVE seed(id)") {
			return strings.Count(q[:strings.Index(q, "chain(")], "(?)")
		}
	}
	t.Fatal("no reach query ran")
	return -1
}

// userOf is the principal of a verified user of the account.
func userOf(accountID string, userID string) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: userID, AccountID: accountID}
}

// TestPlaceNamesSendsOnlyRealParentIDs: a top-level item has no parent, and its "" must not reach the reach query: one
// real parent in the list gives one ID in the query, not two.
func TestPlaceNamesSendsOnlyRealParentIDs(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	p := userOf(fx.acc.ID, fx.ann.ID)

	var cq *countingQuerier
	var got map[string]string
	err := fx.s.store.Read(context.Background(), func(q store.Querier) error {
		cq = &countingQuerier{q: q}
		var err error
		got, err = placeNames(context.Background(), cq, p, []string{"", fx.b.ID})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := reachSeeds(t, cq.queries); n != 1 {
		t.Errorf("the reach query was given %d IDs for one real parent, want 1", n)
	}
	if got[fx.b.ID] != "b" {
		t.Errorf("the name of parent b is %q, want %q", got[fx.b.ID], "b")
	}
}

// TestPlacesIsAFixedNumberOfQueries: a page of items costs the same queries however long it is, and a user's page at
// most three: the user row, the reach of the parents, and their names. Items alternate between a child of b (named) and
// a child of a (out of reach), so both answers are checked at each length.
func TestPlacesIsAFixedNumberOfQueries(t *testing.T) {
	t.Parallel()
	fx := newSharingFixture(t)
	fx.setPerm(t, fx.b.ID, fx.ann.ID, "viewer")
	p := userOf(fx.acc.ID, fx.ann.ID)

	queriesFor := func(n int) int {
		items := make([]*nodeJSON, n)
		for i := range items {
			parent := fx.b.ID
			if i%2 == 1 {
				parent = fx.a.ID
			}
			items[i] = &nodeJSON{ParentID: &parent}
		}
		var cq *countingQuerier
		var places []string
		err := fx.s.store.Read(context.Background(), func(q store.Querier) error {
			cq = &countingQuerier{q: q}
			var err error
			places, err = placesIn(context.Background(), cq, p, items)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		for i, place := range places {
			want := "b"
			if i%2 == 1 {
				want = ""
			}
			if place != want {
				t.Errorf("%d items: place of item %d = %q, want %q", n, i, place, want)
			}
		}
		return len(cq.queries)
	}
	one, many := queriesFor(1), queriesFor(20)
	if one != many {
		t.Errorf("a page of 1 item ran %d queries, a page of 20 ran %d: the count must not grow with the page", one, many)
	}
	if many > 3 {
		t.Errorf("a page ran %d queries, want at most 3", many)
	}
}

// TestPlaceFollowsAnOrganisationLinkBothWays: a visit to an organisation link reaches the node and what is below it, and
// nothing above it. ann opens a link on deep: deep is hers, and its parent b is not, so deep's place is "". ann opens a
// link on b with a grant on deep: deep's parent b is reached through the link, so deep's place is "b", and b's parent a
// is not, so b's place is "".
func TestPlaceFollowsAnOrganisationLinkBothWays(t *testing.T) {
	t.Parallel()

	t.Run("a link on the file does not reach its parent", func(t *testing.T) {
		t.Parallel()
		fx := newLinkFixture(t)
		_, url := fx.makeLink(t, fx.deep.ID, "viewer")
		fx.openedAs(t, fx.annSess, secretOf(t, url))

		got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
		wantPlaces(t, got, map[string]string{"file.txt": ""})
	})

	t.Run("a link on the parent reaches the file's place", func(t *testing.T) {
		t.Parallel()
		fx := newLinkFixture(t)
		fx.setPerm(t, fx.deep.ID, fx.ann.ID, "viewer")
		_, url := fx.makeLink(t, fx.b.ID, "viewer")
		fx.openedAs(t, fx.annSess, secretOf(t, url))

		got := placesOf(t, callSession(t, fx.s, fx.annSess, "GET", "/api/v1/shared", nil))
		wantPlaces(t, got, map[string]string{"file.txt": "b", "b": ""})
	})
}
