// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// filteredFixture is searchFixture with five children of fld_a's root: two folders' worth of kinds and types, with
// modified times 1000, 2000 and 3000 on the three files.
func filteredFixture(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, _ := searchFixture(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	file := func(id, name, mime string) *Node {
		n := node(id, "fld_a", "nod_root", name, "file")
		n.MimeType = mime
		return n
	}
	must(s.Write(ctx, func(tx *Tx) error {
		if err := tx.InsertNode(ctx, file("nod_pdf", "a.pdf", "application/pdf")); err != nil {
			return err
		}
		if err := tx.InsertNode(ctx, file("nod_docx", "b.docx", "")); err != nil {
			return err
		}
		if err := tx.InsertNode(ctx, file("nod_jpg", "c.jpg", "image/jpeg")); err != nil {
			return err
		}
		return tx.InsertNode(ctx, node("nod_dir", "fld_a", "nod_root", "Folder", "dir"))
	}))
	// A shortcut has no group; its row is written directly, as the API writes it through its own route.
	must(s.Write(ctx, func(tx *Tx) error {
		_, err := tx.q.ExecContext(ctx, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at, type_group)
			VALUES ('nod_link', 'acc_a', 'fld_a', 'nod_root', 'Link', 'Link', 'shortcut', 0, '', 0, 0, '')`)
		return err
	}))
	for id, at := range map[string]int64{"nod_pdf": 1000, "nod_docx": 2000, "nod_jpg": 3000} {
		must(exec(s, `UPDATE nodes SET modified_at = ? WHERE id = ?`, at, id))
	}
	return s
}

func exec(s *Store, q string, args ...any) error {
	_, err := s.w.ExecContext(context.Background(), q, args...)
	return err
}

func nameList(kids []*Node) []string {
	var out []string
	for _, k := range kids {
		out = append(out, k.Name)
	}
	return out
}

func listFiltered(t *testing.T, s *Store, order ChildSort, limit int, after *ChildPos, f ListFilter) ([]*Node, bool) {
	t.Helper()
	kids, more, err := s.ChildrenFiltered(context.Background(), "acc_a", "nod_root", order, limit, after, f)
	if err != nil {
		t.Fatal(err)
	}
	return kids, more
}

// TestChildrenFilteredByType: a file group keeps the files of that group; folder and shortcut keep their kind; the zero
// filter keeps every child, in the order the sorted walk gives (folders, shortcuts, then files).
func TestChildrenFilteredByType(t *testing.T) {
	s := filteredFixture(t)
	by := ChildSort{Key: SortName}
	cases := []struct {
		name string
		f    ListFilter
		want []string
	}{
		{"zero filter", ListFilter{}, []string{"Folder", "Link", "a.pdf", "b.docx", "c.jpg"}},
		{"pdf", ListFilter{Group: "pdf"}, []string{"a.pdf"}},
		{"wordprocessing", ListFilter{Group: "wordprocessing"}, []string{"b.docx"}},
		{"image", ListFilter{Group: "image"}, []string{"c.jpg"}},
		{"folder", ListFilter{Kind: KindDir}, []string{"Folder"}},
		{"shortcut", ListFilter{Kind: KindShortcut}, []string{"Link"}},
		{"no match", ListFilter{Group: "audio"}, nil},
	}
	for _, c := range cases {
		kids, more := listFiltered(t, s, by, 10, nil, c.f)
		if more {
			t.Errorf("%s: more with a limit of 10", c.name)
		}
		if got := nameList(kids); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestChildrenFilteredByModifiedRange: after is inclusive and before exclusive, in milliseconds.
func TestChildrenFilteredByModifiedRange(t *testing.T) {
	s := filteredFixture(t)
	f := ListFilter{ModAfter: i64p(2000), ModBefore: i64p(3000)}
	kids, _ := listFiltered(t, s, ChildSort{Key: SortName}, 10, nil, f)
	if got := nameList(kids); !reflect.DeepEqual(got, []string{"b.docx"}) {
		t.Errorf("[2000, 3000): %v, want [b.docx]", got)
	}
	kids, _ = listFiltered(t, s, ChildSort{Key: SortName}, 10, nil, ListFilter{ModAfter: i64p(2000)})
	// The range applies to every kind: the folder is modified at 1000 and the shortcut at 0, so neither is from 2000 on.
	if got := nameList(kids); !reflect.DeepEqual(got, []string{"b.docx", "c.jpg"}) {
		t.Errorf("from 2000 on: %v, want [b.docx c.jpg]", got)
	}
}

// TestChildrenFilteredPagesOnce: a filtered walk over several pages returns every match exactly once, in order, and
// reports more until the last one.
func TestChildrenFilteredPagesOnce(t *testing.T) {
	s := filteredFixture(t)
	if err := s.Write(context.Background(), func(tx *Tx) error {
		for _, n := range []string{"d1.pdf", "d2.pdf", "d3.pdf"} {
			if err := tx.InsertNode(context.Background(), node("nod_"+n, "fld_a", "nod_root", n, "file")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := ListFilter{Group: "pdf"}
	var got []string
	var after *ChildPos
	for pages := 0; pages < 10; pages++ {
		kids, more := listFiltered(t, s, ChildSort{Key: SortName}, 2, after, f)
		got = append(got, nameList(kids)...)
		if !more {
			break
		}
		pos := ChildSort{Key: SortName}.PosOf(kids[len(kids)-1])
		after = &pos
	}
	want := []string{"a.pdf", "d1.pdf", "d2.pdf", "d3.pdf"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pages: %v, want %v", got, want)
	}
}

// TestChildrenFilteredSizeOrder: the sorted walk keeps its order under a filter (here size, descending, over the
// files of one group).
func TestChildrenFilteredSizeOrder(t *testing.T) {
	s := filteredFixture(t)
	if err := exec(s, `UPDATE nodes SET size = 5 WHERE id = 'nod_pdf'`); err != nil {
		t.Fatal(err)
	}
	if err := exec(s, `UPDATE nodes SET size = 9 WHERE id = 'nod_jpg'`); err != nil {
		t.Fatal(err)
	}
	kids, _ := listFiltered(t, s, ChildSort{Key: SortSize, Desc: true}, 10, nil, ListFilter{})
	var files []string
	for _, k := range kids {
		if k.Kind == KindFile {
			files = append(files, k.Name)
		}
	}
	// b.docx keeps the helper's size of 10, which is above c.jpg's 9 and a.pdf's 5.
	if !reflect.DeepEqual(files, []string{"b.docx", "c.jpg", "a.pdf"}) {
		t.Errorf("size desc: %v", files)
	}
}

// TestChildrenFilteredBadCursor: a position whose kind is not a segment of the walk is ErrBadCursor, filter or not.
func TestChildrenFilteredBadCursor(t *testing.T) {
	s := filteredFixture(t)
	_, _, err := s.ChildrenFiltered(context.Background(), "acc_a", "nod_root", ChildSort{Key: SortName}, 2,
		&ChildPos{Kind: "nonsense", Text: "a"}, ListFilter{Group: "pdf"})
	if !errors.Is(err, ErrBadCursor) {
		t.Errorf("err %v, want ErrBadCursor", err)
	}
}
