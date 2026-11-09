// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"strings"
	"testing"
)

func i64p(v int64) *int64 { return &v }

// TestListTypeFilterValues: a type value is a group ID, folder or shortcut; anything else is ErrBadFilter, and the
// empty value is no filter.
func TestListTypeFilterValues(t *testing.T) {
	cases := []struct {
		in   string
		want ListFilter
	}{
		{"", ListFilter{}},
		{"wordprocessing", ListFilter{Group: "wordprocessing"}},
		{"pdf", ListFilter{Group: "pdf"}},
		{"folder", ListFilter{Kind: KindDir}},
		{"shortcut", ListFilter{Kind: KindShortcut}},
	}
	for _, c := range cases {
		got, err := ListTypeFilter(c.in)
		if err != nil {
			t.Errorf("ListTypeFilter(%q): %v", c.in, err)
			continue
		}
		if got.Kind != c.want.Kind || got.Group != c.want.Group {
			t.Errorf("ListTypeFilter(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"documents", "PDF", "file", "dir", "audio "} {
		if _, err := ListTypeFilter(bad); !errors.Is(err, ErrBadFilter) {
			t.Errorf("ListTypeFilter(%q) = %v, want ErrBadFilter", bad, err)
		}
	}
}

// TestListFilterBoundsOrder: modified_after must be before modified_before; equal bounds or a reversed pair are refused.
func TestListFilterBoundsOrder(t *testing.T) {
	ok := ListFilter{ModAfter: i64p(1), ModBefore: i64p(2)}
	if err := ok.Validate(); err != nil {
		t.Errorf("1 < 2: %v", err)
	}
	for _, f := range []ListFilter{
		{ModAfter: i64p(2), ModBefore: i64p(2)},
		{ModAfter: i64p(3), ModBefore: i64p(2)},
	} {
		if err := f.Validate(); !errors.Is(err, ErrBadFilter) {
			t.Errorf("bounds %d, %d: %v, want ErrBadFilter", *f.ModAfter, *f.ModBefore, err)
		}
	}
	if err := (ListFilter{ModAfter: i64p(5)}).Validate(); err != nil {
		t.Errorf("one bound alone: %v", err)
	}
}

// TestListFilterActiveAndTag: a zero filter is inactive and has no tag; each set part changes the tag, and the same
// filter always gives the same tag.
func TestListFilterActiveAndTag(t *testing.T) {
	if (ListFilter{}).Active() {
		t.Error("the zero filter is active")
	}
	if tag := (ListFilter{}).Tag(); tag != "" {
		t.Errorf("zero tag = %q, want empty", tag)
	}
	a := ListFilter{Group: "pdf", ModAfter: i64p(10)}
	if !a.Active() || a.Tag() == "" {
		t.Error("a set filter is inactive or untagged")
	}
	if a.Tag() != (ListFilter{Group: "pdf", ModAfter: i64p(10)}).Tag() {
		t.Error("the same filter gave two tags")
	}
	others := []ListFilter{
		{Group: "image", ModAfter: i64p(10)},
		{Group: "pdf", ModAfter: i64p(11)},
		{Group: "pdf", ModBefore: i64p(10)},
		{Kind: KindDir, ModAfter: i64p(10)},
	}
	for _, o := range others {
		if o.Tag() == a.Tag() {
			t.Errorf("filter %+v has the tag of %+v", o, a)
		}
	}
}

// TestListFilterWhereIsBound: the clauses name the columns of one alias and carry every value as an argument; a group
// value is never in the text.
func TestListFilterWhereIsBound(t *testing.T) {
	sqlText, args := ListFilter{Group: "pdf", ModAfter: i64p(10), ModBefore: i64p(20)}.Where("n")
	want := " AND n.kind = 'file' AND n.type_group = ? AND n.modified_at >= ? AND n.modified_at < ?"
	if sqlText != want {
		t.Errorf("Where = %q, want %q", sqlText, want)
	}
	if len(args) != 3 || args[0] != "pdf" || args[1] != int64(10) || args[2] != int64(20) {
		t.Errorf("args = %v", args)
	}
	if strings.Contains(sqlText, "pdf") {
		t.Error("a group value is in the SQL text")
	}
	if sqlText, args := (ListFilter{Kind: KindDir}).Where("n"); sqlText != " AND n.kind = ?" || len(args) != 1 || args[0] != KindDir {
		t.Errorf("kind filter: %q %v", sqlText, args)
	}
	if sqlText, args := (ListFilter{}).Where("n"); sqlText != "" || len(args) != 0 {
		t.Errorf("zero filter: %q %v", sqlText, args)
	}
}
