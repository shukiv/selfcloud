// SPDX-License-Identifier: AGPL-3.0-or-later

package templates

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped set (nt2): the LibreOffice templates embedded in files/, their pictures in thumbs/, and the credits. The
// expected values come from the phase-1 design: IDs builtin:lo-<name>, the English titles, the categories.

// shippedNames are the eighteen shipped files, by name (without the lo- prefix in the ID), with their English titles.
var shippedNames = []struct{ name, title, category string }{
	{"lo-letter-serif", "Modern business letter serif", "letters"},
	{"lo-letter-sans-serif", "Modern business letter sans-serif", "letters"},
	{"lo-beehive", "Beehive", "designs"},
	{"lo-blue-curve", "Blue Curve", "designs"},
	{"lo-dna", "DNA", "designs"},
	{"lo-focus", "Focus", "designs"},
	{"lo-forestbird", "Forestbird", "designs"},
	{"lo-inspiration", "Inspiration", "designs"},
	{"lo-lights", "Lights", "designs"},
	{"lo-metropolis", "Metropolis", "designs"},
	{"lo-midnightblue", "Midnightblue", "designs"},
	{"lo-nature-illustration", "Nature Illustration", "designs"},
	{"lo-pencil", "Pencil", "designs"},
	{"lo-piano", "Piano", "designs"},
	{"lo-portfolio", "Portfolio", "designs"},
	{"lo-progress", "Progress", "designs"},
	{"lo-sunset", "Sunset", "designs"},
	{"lo-vivid", "Vivid", "designs"},
}

// The main content type each shipped format declares in [Content_Types].xml.
var mainTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml",
	"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml",
}

// The categories of the built-ins (nt2, phase 1): letters, resumes, reports, workbooks, presentations, designs.
func TestBuiltInsHaveTheirCategory(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"builtin:letter":        "letters",
		"builtin:resume":        "resumes",
		"builtin:meeting-notes": "reports",
		"builtin:budget":        "workbooks",
		"builtin:invoice":       "workbooks",
		"builtin:slide-deck":    "presentations",
	}
	for _, s := range shippedNames {
		want["builtin:"+s.name] = s.category
	}
	if len(want) != 24 {
		t.Fatalf("the expected set has %d entries, want 24", len(want))
	}
	for _, tpl := range All() {
		cat, ok := want[tpl.ID]
		if !ok {
			t.Errorf("%s has no expected category", tpl.ID)
			continue
		}
		if got := tpl.Category; got != cat {
			t.Errorf("%s category = %q, want %q", tpl.ID, got, cat)
		}
	}
}

// Each shipped template has its English title, and the English title is the name in every language (no catalogue).
func TestShippedNamesAreEnglishAndFallBack(t *testing.T) {
	t.Parallel()
	for _, s := range shippedNames {
		tpl, ok := Get("builtin:" + s.name)
		if !ok {
			t.Fatalf("no builtin:%s", s.name)
		}
		if got := tpl.Name("en"); got != s.title {
			t.Errorf("%s en name = %q, want %q", tpl.ID, got, s.title)
		}
		for _, lang := range Langs {
			if got := tpl.Name(lang); got != s.title {
				t.Errorf("%s name in %s = %q, want the English fallback %q", tpl.ID, lang, got, s.title)
			}
		}
	}
}

// Every shipped file is in the set, and every shipped ID has its file: the embedded files and the set agree.
func TestEveryShippedFileIsABuiltIn(t *testing.T) {
	t.Parallel()
	for _, s := range shippedNames {
		if _, ok := Get("builtin:" + s.name); !ok {
			t.Errorf("shipped file %s is not a built-in", s.name)
		}
	}
	if n := len(All()); n != 6+len(shippedNames) {
		t.Errorf("All() has %d templates, want %d", n, 6+len(shippedNames))
	}
}

// Each shipped file opens as a zip package of its format: the main part is there and its content type is the format's.
func TestShippedFilesOpenAsTheirMediaType(t *testing.T) {
	t.Parallel()
	for _, s := range shippedNames {
		tpl, _ := Get("builtin:" + s.name)
		body, err := tpl.Build()
		if err != nil {
			t.Fatalf("%s: Build: %v", tpl.ID, err)
		}
		files := parts(t, body)
		ct := string(files["[Content_Types].xml"])
		want, ok := mainTypes[tpl.Ext]
		if !ok {
			t.Fatalf("%s: no media type for %s", tpl.ID, tpl.Ext)
		}
		if !strings.Contains(ct, want) {
			t.Errorf("%s: [Content_Types].xml does not declare %s", tpl.ID, want)
		}
	}
}

// The shipped files carry no author: docProps/core.xml names no e-mail address, no creator and no last editor.
func TestShippedFilesCarryNoAuthor(t *testing.T) {
	t.Parallel()
	for _, s := range shippedNames {
		tpl, _ := Get("builtin:" + s.name)
		body, err := tpl.Build()
		if err != nil {
			t.Fatal(err)
		}
		core, ok := parts(t, body)["docProps/core.xml"]
		if !ok {
			t.Errorf("%s: no docProps/core.xml", tpl.ID)
			continue
		}
		if bytes.Contains(core, []byte("@")) {
			t.Errorf("%s: docProps/core.xml holds an \"@\"", tpl.ID)
		}
		for _, tag := range []string{"<dc:creator", "<cp:lastModifiedBy"} {
			if bytes.Contains(core, []byte(tag)) {
				t.Errorf("%s: docProps/core.xml has %s", tpl.ID, tag)
			}
		}
	}
}

// Every built-in has a picture: a PNG, by its signature, and the six of ours are shipped with the shipped set.
func TestEveryBuiltInHasAPNGThumbnail(t *testing.T) {
	t.Parallel()
	sig := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	for _, tpl := range All() {
		data, ok := Thumbnail(tpl.ID)
		if !ok {
			t.Errorf("%s has no thumbnail", tpl.ID)
			continue
		}
		if !bytes.HasPrefix(data, sig) {
			t.Errorf("%s: thumbnail is not a PNG", tpl.ID)
		}
	}
}

// A thumbnail is answered for a built-in only: an account's node ID and an unknown ID have none.
func TestThumbnailIsOnlyForBuiltIns(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"nod_abc", "builtin:nope", "builtin:", "", "lo-beehive"} {
		if _, ok := Thumbnail(id); ok {
			t.Errorf("Thumbnail(%q) answered a picture", id)
		}
	}
}

// Every shipped file and picture is in the repository, and the credits name each one. The credits file is at the root.
func TestCreditsNameEveryShippedFile(t *testing.T) {
	t.Parallel()
	credits, err := os.ReadFile(filepath.Join("..", "..", "CREDITS.md"))
	if err != nil {
		t.Fatalf("CREDITS.md at the repository root: %v", err)
	}
	text := string(credits)
	for _, s := range shippedNames {
		if !strings.Contains(text, s.name+".") && !strings.Contains(text, s.name+".docx") && !strings.Contains(text, s.name+".pptx") {
			t.Errorf("CREDITS.md does not name %s", s.name)
		}
	}
	for _, want := range []string{
		"400f1532980458382365ab7cd420b161eea40a40",
		"extras/source/templates/presnt/Beehive",
		"officorr/Modern_business_letter_serif",
		"https://creativecommons.org/publicdomain/zero/1.0/",
		"MPL-2.0",
		"Not shipped",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("CREDITS.md does not contain %q", want)
		}
	}
	for _, bad := range []string{"/home", "@"} {
		if strings.Contains(text, bad) {
			t.Errorf("CREDITS.md contains %q", bad)
		}
	}
}

// The MPL-2.0 text is shipped beside the credits, as LibreOffice's COPYING.MPL.
func TestMPLTextIsShipped(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("licenses", "MPL-2.0.txt"))
	if err != nil {
		t.Fatalf("licenses/MPL-2.0.txt: %v", err)
	}
	if !bytes.Contains(data, []byte("Mozilla Public License Version 2.0")) {
		t.Error("licenses/MPL-2.0.txt is not the MPL 2.0 text")
	}
}
