// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// TestChildrenSortRejectsUnknownValues: a sort or an order outside the fixed list is invalid_request, and so is a repeated
// parameter. None of them reaches SQL.
func TestChildrenSortRejectsUnknownValues(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	base := "/api/v1/folders/" + f.ID + "/children"
	for _, q := range []string{
		"sort=owner",
		"sort=",
		"sort=name%20desc",
		"sort=size&order=up",
		"sort=size&order=",
		"sort=size&sort=name",
		"order=desc&order=asc",
		"sort=name%27%20OR%201%3D1",
	} {
		wantError(t, call(t, s, "GET", base+"?"+q, key, nil), 400, apierr.InvalidRequest)
	}
}

// TestChildrenDefaultOrderIsUnchanged: with neither sort nor order the listing answers in the name-key order it always
// had, folders and files together.
func TestChildrenDefaultOrderIsUnchanged(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	mkdirs(t, s, key, f.ID, "/beta")
	putNode(t, s, key, f.ID, "/alpha.txt", "aa")
	putNode(t, s, key, f.ID, "/file10.txt", "a")
	putNode(t, s, key, f.ID, "/file2.txt", "aaaaaaaaaa")
	got := decode[listResponse[nodeJSON]](t, call(t, s, "GET", "/api/v1/folders/"+f.ID+"/children", key, nil))
	var names []string
	for _, n := range got.Items {
		names = append(names, n.Name)
	}
	// Byte order of the name keys: "alpha.txt", "beta", "file10.txt", "file2.txt".
	if want := []string{"alpha.txt", "beta", "file10.txt", "file2.txt"}; !slices.Equal(names, want) {
		t.Fatalf("default order = %v, want %v", names, want)
	}
}

// walkSorted reads a sorted listing a page at a time over HTTP and returns the names in the order read.
func walkSorted(t *testing.T, s *Server, key, base, q string, limit int) []string {
	t.Helper()
	var names []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 1000 {
			t.Fatalf("walk did not end after %d pages", pages)
		}
		u := fmt.Sprintf("%s?%s&limit=%d", base, q, limit)
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		page := decode[listResponse[nodeJSON]](t, call(t, s, "GET", u, key, nil))
		for _, n := range page.Items {
			names = append(names, n.Name)
		}
		if page.NextCursor == "" {
			return names
		}
		cursor = page.NextCursor
	}
}

// TestChildrenSortedWalkPutsLargestFirst: the largest file is named last, so a listing that sorts only the loaded rows
// would show it on a later page. Sorted by size, descending, it is the first row of the first page, and a walk
// reads every file once in size order. The directory comes first, as it does in the web listing.
func TestChildrenSortedWalkPutsLargestFirst(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	mkdirs(t, s, key, f.ID, "/a-folder")
	var want []string
	for i := 1; i <= 25; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		putNode(t, s, key, f.ID, "/"+name, strings.Repeat("x", i))
		want = append(want, name)
	}
	putNode(t, s, key, f.ID, "/zz-largest.bin", strings.Repeat("x", 500))
	base := "/api/v1/folders/" + f.ID + "/children"

	first := decode[listResponse[nodeJSON]](t, call(t, s, "GET", base+"?sort=size&order=desc&limit=4", key, nil))
	if len(first.Items) != 4 || first.Items[0].Kind != "dir" {
		t.Fatalf("first page = %+v", first.Items)
	}
	all := walkSorted(t, s, key, base, "sort=size&order=desc", 4)
	if len(all) != 27 || all[0] != "a-folder" || all[1] != "zz-largest.bin" || all[2] != "f25.txt" || all[26] != "f01.txt" {
		t.Fatalf("size desc walk = %v", all)
	}
	asc := walkSorted(t, s, key, base, "sort=size&order=asc", 4)
	if asc[1] != "f01.txt" || asc[25] != "f25.txt" || asc[26] != "zz-largest.bin" {
		t.Fatalf("size asc walk = %v", asc)
	}
	// The name order is by the collation: "f02.txt" before "f10.txt" and "zz-largest.bin" last.
	byName := walkSorted(t, s, key, base, "sort=name", 5)
	if byName[0] != "a-folder" || byName[1] != "f01.txt" || byName[2] != "f02.txt" || byName[26] != "zz-largest.bin" {
		t.Fatalf("name walk = %v", byName)
	}
}

// TestChildrenSortedCursorRefusesAnotherWalk: a cursor opens only for the sort and order it was made for, and for the
// same directory. A cursor from the default order does not open for a sorted listing, nor the reverse.
func TestChildrenSortedCursorRefusesAnotherWalk(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	for i := 1; i <= 6; i++ {
		putNode(t, s, key, f.ID, fmt.Sprintf("n%d.txt", i), strings.Repeat("y", i))
	}
	base := "/api/v1/folders/" + f.ID + "/children"
	sized := decode[listResponse[nodeJSON]](t, call(t, s, "GET", base+"?sort=size&limit=2", key, nil))
	plain := decode[listResponse[nodeJSON]](t, call(t, s, "GET", base+"?limit=2", key, nil))
	if sized.NextCursor == "" || plain.NextCursor == "" {
		t.Fatal("expected a second page for both walks")
	}
	cur := url.QueryEscape(sized.NextCursor)
	wantError(t, call(t, s, "GET", base+"?sort=modified&limit=2&cursor="+cur, key, nil), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "GET", base+"?sort=size&order=desc&limit=2&cursor="+cur, key, nil), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "GET", base+"?sort=name&limit=2&cursor="+cur, key, nil), 400, apierr.InvalidRequest)
	wantError(t, call(t, s, "GET", base+"?limit=2&cursor="+cur, key, nil), 400, apierr.InvalidRequest)
	// A sealed cursor used to open on the default walk when its bytes happened to split in two at a NUL. Each seal has its
	// own nonce, so many seals cover that chance: every one must be refused by the default walk.
	for range 50 {
		again := decode[listResponse[nodeJSON]](t, call(t, s, "GET", base+"?sort=size&limit=2", key, nil))
		wantError(t, call(t, s, "GET", base+"?limit=2&cursor="+url.QueryEscape(again.NextCursor), key, nil), 400, apierr.InvalidRequest)
	}
	wantError(t, call(t, s, "GET", base+"?sort=size&limit=2&cursor="+url.QueryEscape(plain.NextCursor), key, nil), 400, apierr.InvalidRequest)
	// The same walk still opens.
	wantStatus(t, call(t, s, "GET", base+"?sort=size&limit=2&cursor="+cur, key, nil), 200)
}
