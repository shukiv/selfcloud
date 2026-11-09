// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/url"
	"slices"
	"testing"

	"github.com/shukiv/selfcloud/internal/apierr"
	"github.com/shukiv/selfcloud/internal/mail"
)

// listNames returns the names of one page of a listing, in order.
func listNames(t *testing.T, s *Server, key, u string) ([]string, string) {
	t.Helper()
	page := decode[listResponse[nodeJSON]](t, call(t, s, "GET", u, key, nil))
	var names []string
	for _, n := range page.Items {
		names = append(names, n.Name)
	}
	return names, page.NextCursor
}

// TestChildrenFilterByType: a folder's listing takes ?type=; a file group keeps the files of the group, and folder keeps
// the folders. Every node in the answer carries its type.
func TestChildrenFilterByType(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	putNode(t, s, key, f.ID, "/a.pdf", "aa")
	putNode(t, s, key, f.ID, "/b.docx", "bb")
	mkdirs(t, s, key, f.ID, "/Dir")
	base := "/api/v1/folders/" + f.ID + "/children"
	cases := []struct {
		typ  string
		want []string
	}{
		{"pdf", []string{"a.pdf"}},
		{"wordprocessing", []string{"b.docx"}},
		{"folder", []string{"Dir"}},
		{"audio", nil},
	}
	for _, c := range cases {
		got, _ := listNames(t, s, key, base+"?type="+c.typ)
		if !slices.Equal(got, c.want) {
			t.Errorf("type=%s: %v, want %v", c.typ, got, c.want)
		}
	}
	page := decode[listResponse[nodeJSON]](t, call(t, s, "GET", base+"?type=pdf", key, nil))
	if len(page.Items) != 1 || page.Items[0].Type != "pdf" {
		t.Errorf("the node's type in the answer: %+v", page.Items)
	}
}

// TestChildrenFilterRejectsBadValues: an unknown type, a repeated type, a bad modified bound, a negative bound, and a
// range whose start is not before its end are each invalid_request, with no list.
func TestChildrenFilterRejectsBadValues(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	base := "/api/v1/folders/" + f.ID + "/children"
	for _, q := range []string{
		"type=documents",
		"type=PDF",
		"type=pdf&type=image",
		"modified_after=x",
		"modified_after=-1",
		"modified_before=-5",
		"modified_after=5&modified_before=5",
		"modified_after=6&modified_before=5",
	} {
		wantError(t, call(t, s, "GET", base+"?"+q, key, nil), 400, apierr.InvalidRequest)
	}
}

// TestChildrenFilterCursorRules: a filtered walk over several pages returns every match once. A cursor of the sorted
// walk without the filter, a cursor of the filter without the filter, and a plain cursor with a filter are each 400.
func TestChildrenFilterCursorRules(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	for _, n := range []string{"/a.pdf", "/b.pdf", "/c.pdf", "/d.docx"} {
		putNode(t, s, key, f.ID, n, "xx")
	}
	base := "/api/v1/folders/" + f.ID + "/children"

	walked := walkSorted(t, s, key, base, "type=pdf", 1)
	if want := []string{"a.pdf", "b.pdf", "c.pdf"}; !slices.Equal(walked, want) {
		t.Errorf("filtered walk: %v, want %v", walked, want)
	}

	// The first page of the sorted walk with no filter, then its cursor sent with the filter.
	_, sortedNext := listNames(t, s, key, base+"?sort=name&limit=1")
	if sortedNext == "" {
		t.Fatal("no sorted cursor to test with")
	}
	wantError(t, call(t, s, "GET", base+"?type=pdf&sort=name&limit=1&cursor="+url.QueryEscape(sortedNext), key, nil),
		400, apierr.InvalidRequest)

	// A filtered cursor sent without its filter.
	_, pdfNext := listNames(t, s, key, base+"?type=pdf&limit=1")
	if pdfNext == "" {
		t.Fatal("no filtered cursor to test with")
	}
	wantError(t, call(t, s, "GET", base+"?sort=name&limit=1&cursor="+url.QueryEscape(pdfNext), key, nil),
		400, apierr.InvalidRequest)

	// A plain cursor (the unsorted walk's) sent with a filter.
	_, plainNext := listNames(t, s, key, base+"?limit=1")
	if plainNext == "" {
		t.Fatal("no plain cursor to test with")
	}
	wantError(t, call(t, s, "GET", base+"?type=pdf&limit=1&cursor="+url.QueryEscape(plainNext), key, nil),
		400, apierr.InvalidRequest)
}

// TestChildrenFilterForcesSortedWalk: a filter with no sort lists in name order, as the sorted walk does, even where
// the unsorted walk would order the same rows differently.
func TestChildrenFilterForcesSortedWalk(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	for _, n := range []string{"/file10.pdf", "/file2.pdf", "/alpha.pdf"} {
		putNode(t, s, key, f.ID, n, "x")
	}
	got, _ := listNames(t, s, key, "/api/v1/folders/"+f.ID+"/children?type=pdf")
	// The sorted walk orders names by the server's name collation, which reads the number in "file2" before "file10".
	if want := []string{"alpha.pdf", "file2.pdf", "file10.pdf"}; !slices.Equal(got, want) {
		t.Errorf("filtered, no sort: %v, want %v", got, want)
	}
}

// TestChildrenFilterModifiedRange: a range the files are not in lists none; the zero range lists them all.
func TestChildrenFilterModifiedRange(t *testing.T) {
	t.Parallel()
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	f := newFolder(t, s, key, acc.ID, "Docs")
	putNode(t, s, key, f.ID, "/a.pdf", "aa")
	base := "/api/v1/folders/" + f.ID + "/children"
	if got, _ := listNames(t, s, key, base+"?modified_before=1"); len(got) != 0 {
		t.Errorf("modified before 1 ms: %v, want none", got)
	}
	if got, _ := listNames(t, s, key, base+"?modified_after=1"); !slices.Equal(got, []string{"a.pdf"}) {
		t.Errorf("modified after 1 ms: %v, want [a.pdf]", got)
	}
}
