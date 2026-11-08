// SPDX-License-Identifier: AGPL-3.0-or-later

package templates

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// parts reads a package into its parts by name.
func parts(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip package: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = data
	}
	return out
}

// wellFormed fails when data is not XML that decodes to its end.
func wellFormed(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// The built-in set is fixed: these IDs, these kinds, these extensions. A change to the set is a change to this test.
func TestBuiltInSetIsFixed(t *testing.T) {
	t.Parallel()
	want := []struct{ id, kind, ext string }{
		{"builtin:letter", Document, "docx"},
		{"builtin:resume", Document, "docx"},
		{"builtin:meeting-notes", Document, "docx"},
		{"builtin:budget", Spreadsheet, "xlsx"},
		{"builtin:invoice", Spreadsheet, "xlsx"},
		{"builtin:slide-deck", Presentation, "pptx"},
	}
	all := All()
	if len(all) != len(want) {
		t.Fatalf("All() has %d templates, want %d", len(all), len(want))
	}
	for i, w := range want {
		if all[i].ID != w.id || all[i].Kind != w.kind || all[i].Ext != w.ext {
			t.Errorf("template %d = %s %s %s, want %s %s %s", i, all[i].ID, all[i].Kind, all[i].Ext, w.id, w.kind, w.ext)
		}
		got, ok := Get(w.id)
		if !ok || got.ID != w.id {
			t.Errorf("Get(%s) = %v, %v", w.id, got.ID, ok)
		}
	}
	if _, ok := Get("builtin:nope"); ok {
		t.Error("Get of an unknown built-in answered a template")
	}
	if _, ok := Get("nod_abc"); ok {
		t.Error("Get of an account node ID answered a built-in template")
	}
}

// Every built-in template builds a valid package for its kind: the parts its format needs are present and well-formed,
// and the content types declare the main part.
func TestEveryBuiltInBuildsAValidPackage(t *testing.T) {
	t.Parallel()
	main := map[string]string{"docx": "word/document.xml", "xlsx": "xl/workbook.xml", "pptx": "ppt/presentation.xml"}
	for _, tpl := range All() {
		body, err := tpl.Build()
		if err != nil {
			t.Fatalf("%s: Build: %v", tpl.ID, err)
		}
		files := parts(t, body)
		m := main[tpl.Ext]
		if _, ok := files[m]; !ok {
			t.Errorf("%s: no main part %s", tpl.ID, m)
		}
		for name, data := range files {
			if err := wellFormed(data); err != nil {
				t.Errorf("%s: part %s is not well-formed: %v", tpl.ID, name, err)
			}
		}
		if ct := string(files["[Content_Types].xml"]); !strings.Contains(ct, `PartName="/`+m+`"`) {
			t.Errorf("%s: [Content_Types].xml does not declare /%s", tpl.ID, m)
		}
	}
}

// A built-in's text is written here: the document carries its title and body, the workbook's formula cells carry their
// values (so a reader shows totals before any recalculation), and the deck has three slides.
func TestBuiltInsHaveTheirContent(t *testing.T) {
	t.Parallel()
	get := func(id string) map[string][]byte {
		tpl, ok := Get(id)
		if !ok {
			t.Fatalf("no %s", id)
		}
		body, err := tpl.Build()
		if err != nil {
			t.Fatal(err)
		}
		return parts(t, body)
	}
	if doc := string(get("builtin:letter")["word/document.xml"]); !strings.Contains(doc, "Dear ") || !strings.Contains(doc, "<w:sectPr>") {
		t.Errorf("the letter has no salutation or no page setup")
	}
	sheet := string(get("builtin:invoice")["xl/worksheets/sheet1.xml"])
	if !strings.Contains(sheet, "<f>") {
		t.Fatal("the invoice has no formulas")
	}
	for i := 0; i+3 < len(sheet); i++ {
		if strings.HasPrefix(sheet[i:], "</f>") && !strings.HasPrefix(sheet[i+4:], "<v>") {
			t.Errorf("a formula at byte %d has no cached value", i)
		}
	}
	deck := get("builtin:slide-deck")
	for _, s := range []string{"ppt/slides/slide1.xml", "ppt/slides/slide2.xml", "ppt/slides/slide3.xml"} {
		if _, ok := deck[s]; !ok {
			t.Errorf("the deck has no %s", s)
		}
	}
	if pres := string(deck["ppt/presentation.xml"]); strings.Count(pres, "<p:sldId ") != 3 {
		t.Errorf("the deck lists %d slides, want 3", strings.Count(pres, "<p:sldId "))
	}
}

// The same template is the same bytes every time.
func TestBuiltInsAreDeterministic(t *testing.T) {
	t.Parallel()
	for _, tpl := range All() {
		a, err := tpl.Build()
		if err != nil {
			t.Fatal(err)
		}
		b, err := tpl.Build()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s: two builds differ", tpl.ID)
		}
	}
}

// Each built-in has a display name in the seven languages the server ships. he and ar are machine drafts, marked in the
// source. A language with no name of its own gets English.
func TestBuiltInNamesInSevenLanguages(t *testing.T) {
	t.Parallel()
	for _, lang := range []string{"en", "es", "fr", "pt", "ru", "ar", "he"} {
		if len(Langs) == 0 {
			t.Fatal("Langs is empty")
		}
		found := false
		for _, l := range Langs {
			if l == lang {
				found = true
			}
		}
		if !found {
			t.Errorf("Langs does not list %s", lang)
		}
	}
	for _, tpl := range All() {
		for _, lang := range Langs {
			if tpl.Name(lang) == "" {
				t.Errorf("%s has no name in %s", tpl.ID, lang)
			}
		}
		if tpl.Name("xx") != tpl.Name("en") {
			t.Errorf("%s: an unknown language is %q, want the English name %q", tpl.ID, tpl.Name("xx"), tpl.Name("en"))
		}
	}
	if got, _ := Get("builtin:letter"); got.Name("he") == got.Name("en") {
		t.Error("the Hebrew name of the letter is the English one")
	}
}

// ByKind lists the built-ins of one kind, in the set's order, and nothing for another kind.
func TestByKindListsTheBuiltInsOfThatKind(t *testing.T) {
	t.Parallel()
	ids := func(list []Template) []string {
		var out []string
		for _, tpl := range list {
			out = append(out, tpl.ID)
		}
		return out
	}
	if got := strings.Join(ids(ByKind(Document)), ","); got != "builtin:letter,builtin:resume,builtin:meeting-notes" {
		t.Errorf("document templates = %s", got)
	}
	if got := strings.Join(ids(ByKind(Spreadsheet)), ","); got != "builtin:budget,builtin:invoice" {
		t.Errorf("spreadsheet templates = %s", got)
	}
	if got := ByKind("drawing"); len(got) != 0 {
		t.Errorf("drawing templates = %v, want none", ids(got))
	}
}
