// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// largestFixture is filteredFixture with the file sizes the Largest order needs: d.pdf and e.pdf join a.pdf, so the pdf
// group holds three files of sizes 20, 5 and 1, and b.docx and c.jpg hold 10 and 9.
func largestFixture(t *testing.T) *Store {
	t.Helper()
	s := filteredFixture(t)
	if err := s.Write(context.Background(), func(tx *Tx) error {
		for _, n := range []string{"d.pdf", "e.pdf"} {
			if err := tx.InsertNode(context.Background(), node("nod_"+n, "fld_a", "nod_root", n, "file")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for id, size := range map[string]int64{"nod_pdf": 5, "nod_docx": 10, "nod_jpg": 9, "nod_d.pdf": 20, "nod_e.pdf": 1} {
		if err := exec(s, `UPDATE nodes SET size = ? WHERE id = ?`, size, id); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func largestNames(t *testing.T, s *Store, f ListFilter, p Page) ([]string, string) {
	t.Helper()
	files, next, err := s.LargestFiltered(context.Background(), "acc_a", "", "", "", f, p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out, next
}

// TestLargestFilteredByGroupPagesWithTag: a group keeps the files of that group, largest first; each page's cursor
// carries the filter, so it continues only the same filter.
func TestLargestFilteredByGroupPagesWithTag(t *testing.T) {
	s := largestFixture(t)
	f := ListFilter{Group: "pdf"}
	got, next := largestNames(t, s, f, Page{Limit: 2})
	if !reflect.DeepEqual(got, []string{"d.pdf", "a.pdf"}) || next == "" {
		t.Fatalf("first page %v next %q", got, next)
	}
	got, next = largestNames(t, s, f, Page{Limit: 2, Cursor: next})
	if !reflect.DeepEqual(got, []string{"e.pdf"}) || next != "" {
		t.Errorf("second page %v next %q", got, next)
	}
}

// TestLargestFilteredCursorTags: a filtered cursor opens only with the same filter; an unfiltered cursor does not open
// with a filter, and a filtered one does not open without it. Each refusal is ErrBadCursor.
func TestLargestFilteredCursorTags(t *testing.T) {
	s := largestFixture(t)
	_, pdfNext := largestNames(t, s, ListFilter{Group: "pdf"}, Page{Limit: 1})
	_, plainNext := largestNames(t, s, ListFilter{}, Page{Limit: 1})
	if pdfNext == "" || plainNext == "" {
		t.Fatal("no next cursor to test with")
	}
	if _, _, err := s.LargestFiltered(context.Background(), "acc_a", "", "", "", ListFilter{Group: "image"}, Page{Limit: 1, Cursor: pdfNext}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("pdf cursor with image: %v, want ErrBadCursor", err)
	}
	if _, _, err := s.LargestFiltered(context.Background(), "acc_a", "", "", "", ListFilter{Group: "pdf"}, Page{Limit: 1, Cursor: plainNext}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("unfiltered cursor with pdf: %v, want ErrBadCursor", err)
	}
	if _, _, err := s.LargestFiltered(context.Background(), "acc_a", "", "", "", ListFilter{}, Page{Limit: 1, Cursor: pdfNext}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("pdf cursor without a filter: %v, want ErrBadCursor", err)
	}
	// An unfiltered cursor keeps working with no filter (the walk before ff1 is unchanged).
	if got, _ := largestNames(t, s, ListFilter{}, Page{Limit: 1, Cursor: plainNext}); len(got) != 1 {
		t.Errorf("unfiltered cursor with no filter: %v", got)
	}
}

// TestLargestFilteredRefusesKinds: a folder or shortcut type is not a file list's type, so it is ErrBadFilter.
func TestLargestFilteredRefusesKinds(t *testing.T) {
	s := largestFixture(t)
	for _, f := range []ListFilter{{Kind: KindDir}, {Kind: KindShortcut}} {
		if _, _, err := s.LargestFiltered(context.Background(), "acc_a", "", "", "", f, Page{Limit: 5}); !errors.Is(err, ErrBadFilter) {
			t.Errorf("kind %q: %v, want ErrBadFilter", f.Kind, err)
		}
	}
}

// TestLargestFilteredModifiedRange: the range bounds the files' modified times.
func TestLargestFilteredModifiedRange(t *testing.T) {
	s := largestFixture(t)
	// d.pdf, a.pdf and e.pdf were modified at 1000 (a.pdf's fixture time) and d.pdf and e.pdf by the helper at 1000 too.
	got, _ := largestNames(t, s, ListFilter{Group: "pdf", ModBefore: i64p(1500)}, Page{Limit: 10})
	if !reflect.DeepEqual(got, []string{"d.pdf", "a.pdf", "e.pdf"}) {
		t.Errorf("pdf before 1500: %v", got)
	}
	got, _ = largestNames(t, s, ListFilter{Group: "pdf", ModAfter: i64p(1500)}, Page{Limit: 10})
	if len(got) != 0 {
		t.Errorf("pdf from 1500: %v, want none", got)
	}
}

// TestTrashFilteredByGroupAndTag: the trash roots of a group, newest first; a folder type is its own kind; the cursor
// carries the filter (TrashCursorOf), so it opens only with the same one.
func TestTrashFilteredByGroupAndTag(t *testing.T) {
	s := filteredFixture(t)
	ctx := context.Background()
	at := time.UnixMilli(5_000)
	if err := s.Write(ctx, func(tx *Tx) error {
		for _, id := range []string{"nod_pdf", "nod_docx", "nod_dir"} {
			if err := tx.TrashNode(ctx, "acc_a", id, "usr_a", at); err != nil {
				return err
			}
			at = at.Add(time.Second)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	roots, err := s.TrashRootsFiltered(ctx, "acc_a", "", ListFilter{Group: "pdf"}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := nameList(roots); !reflect.DeepEqual(got, []string{"a.pdf"}) {
		t.Errorf("pdf in trash: %v", got)
	}
	roots, err = s.TrashRootsFiltered(ctx, "acc_a", "", ListFilter{Kind: KindDir}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := nameList(roots); !reflect.DeepEqual(got, []string{"Folder"}) {
		t.Errorf("folder in trash: %v", got)
	}
	all, err := s.TrashRootsFiltered(ctx, "acc_a", "", ListFilter{}, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := nameList(all); !reflect.DeepEqual(got, []string{"Folder", "b.docx", "a.pdf"}) {
		t.Errorf("unfiltered trash, newest first: %v", got)
	}

	// The cursor after the first pdf root: a tagged cursor continues the pdf list only.
	pdf := ListFilter{Group: "pdf"}
	first, err := s.TrashRootsFiltered(ctx, "acc_a", "", ListFilter{}, Page{Limit: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("first root: %v %v", first, err)
	}
	tagged := TrashCursorOf(first[0], pdf)
	if _, err := s.TrashRootsFiltered(ctx, "acc_a", "", ListFilter{Group: "image"}, Page{Limit: 1, Cursor: tagged}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("pdf trash cursor with image: %v, want ErrBadCursor", err)
	}
	if _, err := s.TrashRootsFiltered(ctx, "acc_a", "", pdf, Page{Limit: 10, Cursor: TrashCursor(first[0])}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("unfiltered trash cursor with pdf: %v, want ErrBadCursor", err)
	}
}
