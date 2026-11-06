// SPDX-License-Identifier: AGPL-3.0-or-later

package blankdoc

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

// Every kind is a package with the parts it needs, well-formed, with its main part declared and reached from _rels/.rels.
func TestBuildMakesAValidPackageForEachKind(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		main, mainType string
		parts          []string
	}{
		"docx": {"word/document.xml", "wordprocessingml.document.main+xml", nil},
		"xlsx": {"xl/workbook.xml", "spreadsheetml.sheet.main+xml", []string{"xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"}},
		"pptx": {"ppt/presentation.xml", "presentationml.presentation.main+xml", []string{
			"ppt/_rels/presentation.xml.rels", "ppt/slideMasters/slideMaster1.xml", "ppt/slideMasters/_rels/slideMaster1.xml.rels",
			"ppt/slideLayouts/slideLayout1.xml", "ppt/slideLayouts/_rels/slideLayout1.xml.rels",
			"ppt/slides/slide1.xml", "ppt/slides/_rels/slide1.xml.rels", "ppt/theme/theme1.xml"}},
	}
	for ext, c := range cases {
		body, err := Build(ext)
		if err != nil {
			t.Fatalf("Build(%s): %v", ext, err)
		}
		files := parts(t, body)
		for _, p := range append([]string{"[Content_Types].xml", "_rels/.rels", c.main}, c.parts...) {
			data, ok := files[p]
			if !ok {
				t.Errorf("%s: no part %s", ext, p)
				continue
			}
			if err := wellFormed(data); err != nil {
				t.Errorf("%s: part %s is not well-formed: %v", ext, p, err)
			}
		}
		ct := string(files["[Content_Types].xml"])
		if !strings.Contains(ct, `PartName="/`+c.main+`"`) || !strings.Contains(ct, c.mainType) {
			t.Errorf("%s: [Content_Types].xml does not declare /%s as %s", ext, c.main, c.mainType)
		}
		if rels := string(files["_rels/.rels"]); !strings.Contains(rels, `Target="`+c.main+`"`) {
			t.Errorf("%s: _rels/.rels does not point at %s", ext, c.main)
		}
	}
}

// The same kind is the same bytes every time, so a document's content is reproducible.
func TestBuildIsDeterministic(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{"docx", "xlsx", "pptx"} {
		a, err := Build(ext)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Build(ext)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s: two builds differ", ext)
		}
	}
}

// Only the three office kinds are built; anything else is refused.
func TestBuildRefusesOtherKinds(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{"pdf", "", "DOCX", "odt"} {
		if _, err := Build(ext); err == nil {
			t.Errorf("Build(%q) succeeded, want an error", ext)
		}
	}
}

// Each kind has its Office media type.
func TestMIMEOfEachKind(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	}
	for ext, mt := range want {
		if got := MIME(ext); got != mt {
			t.Errorf("MIME(%s) = %q, want %q", ext, got, mt)
		}
	}
}
