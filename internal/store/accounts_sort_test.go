// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// TestAccountsSortedWalk walks the accounts list by name and by creation time, forwards and backwards, a page at a
// time, and checks each walk visits every account once in its order. Names fold case, as the name collation does: the
// names are chosen so that byte order (Banana, apple, cherry) and folded order (apple, Banana, cherry) differ, and a name
// sort without the collation fails here.
func TestAccountsSortedWalk(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := Now()
	// Created in this order: cherry, Banana, apple.
	names := []string{"cherry", "Banana", "apple"}
	err := s.Write(ctx, func(tx *Tx) error {
		for i, n := range names {
			a := newAccount(fmt.Sprintf("acc_%d", i), "", base.Add(time.Duration(i)*time.Second))
			a.Name = n
			if err := tx.InsertAccount(ctx, a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	walk := func(order AccountSort, limit int) []string {
		t.Helper()
		var got []string
		var after *AccountPos
		for range 10 {
			page, more, err := s.AccountsSorted(ctx, AccountFilter{}, order, limit, after)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range page {
				got = append(got, a.Name)
			}
			if !more {
				return got
			}
			last := order.PosOf(page[len(page)-1])
			after = &last
		}
		t.Fatal("the walk did not end")
		return nil
	}

	cases := []struct {
		name  string
		order AccountSort
		want  []string
	}{
		{"name asc", AccountSort{ByName: true}, []string{"apple", "Banana", "cherry"}},
		{"name desc", AccountSort{ByName: true, Desc: true}, []string{"cherry", "Banana", "apple"}},
		{"created asc", AccountSort{}, []string{"cherry", "Banana", "apple"}},
		{"created desc", AccountSort{Desc: true}, []string{"apple", "Banana", "cherry"}},
	}
	for _, c := range cases {
		for _, limit := range []int{1, 2, 10} {
			if got := walk(c.order, limit); !slices.Equal(got, c.want) {
				t.Errorf("%s with pages of %d: %v, want %v", c.name, limit, got, c.want)
			}
		}
	}
}
