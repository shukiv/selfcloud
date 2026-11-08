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
	for _, ext := range []string{"docx", "xlsx", "pptx", "odt", "ods", "odp", "odg"} {
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
	for _, ext := range []string{"pdf", "", "DOCX", "odf", "rtf", "ODT"} {
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

// The OpenDocument kinds and their media types (ODF 1.3 Part 2, section 3.3 and section 4.16.10).
var odfKinds = map[string]struct {
	mime     string
	parts    []string // the parts besides mimetype and META-INF/manifest.xml
	masterPg bool     // a drawing or a presentation: content names a master page that styles.xml defines
}{
	"odt": {"application/vnd.oasis.opendocument.text", []string{"content.xml", "styles.xml"}, false},
	"ods": {"application/vnd.oasis.opendocument.spreadsheet", []string{"content.xml", "styles.xml"}, false},
	"odp": {"application/vnd.oasis.opendocument.presentation", []string{"content.xml", "styles.xml"}, true},
	"odg": {"application/vnd.oasis.opendocument.graphics", []string{"content.xml", "styles.xml"}, true},
}

// An OpenDocument package has mimetype as its first entry, stored with no extra field, holding the media type, at byte 38
// where the format's detectors read it; the manifest names the root and every part, and every part is well-formed XML.
func TestBuildMakesAValidODFPackage(t *testing.T) {
	t.Parallel()
	for ext, c := range odfKinds {
		body, err := Build(ext)
		if err != nil {
			t.Fatalf("Build(%s): %v", ext, err)
		}
		// The local header of the first entry: no extra field, and the media type's bytes start at 38.
		if len(body) < 38+len(c.mime) {
			t.Fatalf("%s: package is %d bytes, too short for a mimetype at byte 38", ext, len(body))
		}
		if extra := int(body[28]) | int(body[29])<<8; extra != 0 {
			t.Errorf("%s: the first local header has an extra field of %d bytes, want none", ext, extra)
		}
		if got := string(body[38 : 38+len(c.mime)]); got != c.mime {
			t.Errorf("%s: bytes 38.. are %q, want the media type %q", ext, got, c.mime)
		}
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("%s: not a zip package: %v", ext, err)
		}
		first := zr.File[0]
		if first.Name != "mimetype" {
			t.Errorf("%s: the first entry is %q, want mimetype", ext, first.Name)
		}
		if first.Method != zip.Store {
			t.Errorf("%s: mimetype is compressed (method %d), want stored (0)", ext, first.Method)
		}
		if len(first.Extra) != 0 {
			t.Errorf("%s: mimetype has an extra field of %d bytes in the central directory", ext, len(first.Extra))
		}
		files := parts(t, body)
		if got := string(files["mimetype"]); got != c.mime {
			t.Errorf("%s: mimetype holds %q, want %q (no trailing newline)", ext, got, c.mime)
		}
		names := map[string]bool{}
		for _, f := range zr.File {
			names[f.Name] = true
		}
		want := append([]string{"mimetype", "META-INF/manifest.xml"}, c.parts...)
		if len(names) != len(want) {
			t.Errorf("%s: %d entries, want exactly %d (%v)", ext, len(names), len(want), want)
		}
		for _, p := range want {
			data, ok := files[p]
			if !ok {
				t.Errorf("%s: no entry %s", ext, p)
				continue
			}
			if p != "mimetype" {
				if err := wellFormed(data); err != nil {
					t.Errorf("%s: %s is not well-formed XML: %v", ext, p, err)
				}
			}
		}
		manifest := string(files["META-INF/manifest.xml"])
		if !strings.Contains(manifest, `manifest:version="1.3"`) {
			t.Errorf("%s: the manifest does not declare version 1.3", ext)
		}
		if !strings.Contains(manifest, `manifest:full-path="/" manifest:media-type="`+c.mime+`"`) {
			t.Errorf("%s: the manifest has no root entry for %s", ext, c.mime)
		}
		for _, p := range c.parts {
			if !strings.Contains(manifest, `manifest:full-path="`+p+`"`) {
				t.Errorf("%s: the manifest does not list %s", ext, p)
			}
		}
		if strings.Contains(manifest, `manifest:full-path="mimetype"`) {
			t.Errorf("%s: the manifest lists mimetype, which the package does not allow", ext)
		}
		if c.masterPg {
			content, styles := string(files["content.xml"]), string(files["styles.xml"])
			if !strings.Contains(content, `draw:master-page-name="Default"`) ||
				!strings.Contains(styles, `<style:master-page style:name="Default" style:page-layout-name="PM1"`) ||
				!strings.Contains(styles, `<style:page-layout style:name="PM1"`) {
				t.Errorf("%s: the page is not tied to a master page, its layout and styles.xml", ext)
			}
		}
	}
}

// The text file is an empty file, not a zip package.
func TestBuildTxtIsAnEmptyFile(t *testing.T) {
	t.Parallel()
	body, err := Build("txt")
	if err != nil {
		t.Fatalf("Build(txt): %v", err)
	}
	if len(body) != 0 {
		t.Errorf("Build(txt) is %d bytes, want an empty file", len(body))
	}
	if MIME("txt") != "text/plain" {
		t.Errorf("MIME(txt) = %q, want text/plain", MIME("txt"))
	}
}

// The OpenDocument and text kinds have their media types.
func TestMIMEOfODFAndTextKinds(t *testing.T) {
	t.Parallel()
	for ext, c := range odfKinds {
		if got := MIME(ext); got != c.mime {
			t.Errorf("MIME(%s) = %q, want %q", ext, got, c.mime)
		}
	}
}
