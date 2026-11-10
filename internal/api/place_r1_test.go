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
