// SPDX-License-Identifier: AGPL-3.0-or-later

package blankdoc

// The OpenDocument kinds (decision 105): a blank text document, spreadsheet, presentation and drawing, and the empty text
// file. The packages follow ODF 1.3 Part 2 (Packages): the mimetype entry is the first entry of the zip, stored, with no
// extra field, and holds the media type alone (section 3.3); the manifest lists the root and each part (section 3.2, and
// the manifest:version of 1.3 in section 4.16.14.2). Each part is written out here as its XML text, and the zip is made in
// the same fixed order and with the same fixed timestamps as the OOXML packages, so the bytes are the same every time.
// Each part is the minimum the format needs: a document body that is empty, one sheet named Sheet1 with no cells, one
// page for a presentation or a drawing, and one page layout and master page (styles.xml) that the page refers to.
// Nothing is checked against a running editor in the source; see decision 105 for what was checked and how.

import (
	"archive/zip"
	"bytes"
	"hash/crc32"
	"sort"
	"time"
)

// The OpenDocument media types, as the suites name them (ODF 1.3, Part 2, section 3.3).
const (
	mimeODT = "application/vnd.oasis.opendocument.text"
	mimeODS = "application/vnd.oasis.opendocument.spreadsheet"
	mimeODP = "application/vnd.oasis.opendocument.presentation"
	mimeODG = "application/vnd.oasis.opendocument.graphics"
	// mimeTxt is the media type of the empty text file (the plain text type that the download path also serves).
	mimeTxt = "text/plain"
)

// odfDoc is one OpenDocument kind: its media type, its body (the content of office:body), and its page.
type odfDoc struct {
	mime, body, width, height, orient string
}

// odfDocs maps each OpenDocument extension to its kind. A drawing and a presentation have one page; a text document and a
// spreadsheet have none beyond the page setup in styles.xml.
var odfDocs = map[string]odfDoc{
	"odt": {mimeODT, `<office:text><text:p/></office:text>`, "21cm", "29.7cm", "portrait"},
	"ods": {mimeODS, `<office:spreadsheet><table:table table:name="Sheet1"/></office:spreadsheet>`, "21cm", "29.7cm", "portrait"},
	"odp": {mimeODP, `<office:presentation><draw:page draw:name="page1" draw:master-page-name="Default"/></office:presentation>`,
		"28cm", "15.75cm", "landscape"},
	"odg": {mimeODG, `<office:drawing><draw:page draw:name="page1" draw:master-page-name="Default"/></office:drawing>`,
		"29.7cm", "21cm", "landscape"},
}

// The namespaces every ODF part declares, so each part is the same kind of root.
const odfNS = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
	`xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" ` +
	`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" ` +
	`xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0" ` +
	`xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" ` +
	`xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" ` +
	`xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0"`

// odfContent is content.xml: the body of the kind, under the version 1.3 root.
func odfContent(body string) string {
	return xmlHead + `<office:document-content ` + odfNS + ` office:version="1.3">` +
		`<office:body>` + body + `</office:body></office:document-content>`
}

// odfStyles is styles.xml: one page layout with the page's size and orientation, and the master page that uses it. The
// page of a drawing or a presentation names this master page.
func odfStyles(width, height, orient string) string {
	return xmlHead + `<office:document-styles ` + odfNS + ` office:version="1.3">` +
		`<office:automatic-styles>` +
		`<style:page-layout style:name="PM1"><style:page-layout-properties fo:page-width="` + width +
		`" fo:page-height="` + height + `" style:print-orientation="` + orient +
		`" fo:margin-top="2cm" fo:margin-bottom="2cm" fo:margin-left="2cm" fo:margin-right="2cm"/></style:page-layout>` +
		`</office:automatic-styles>` +
		`<office:master-styles><style:master-page style:name="Default" style:page-layout-name="PM1"/></office:master-styles>` +
		`</office:document-styles>`
}

// odfManifest is META-INF/manifest.xml: the root (the package's own media type) and each part. It does not list mimetype.
func odfManifest(mime string, parts []string) string {
	s := xmlHead + `<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.3">` +
		`<manifest:file-entry manifest:full-path="/" manifest:media-type="` + mime + `"/>`
	for _, p := range parts {
		s += `<manifest:file-entry manifest:full-path="` + p + `" manifest:media-type="text/xml"/>`
	}
	return s + `</manifest:manifest>`
}

// dosTime is the MS-DOS date and time of t, as the zip format stores them. The mimetype entry carries them alone, so its
// local header has no extended timestamp (an extra field), which the format does not allow for that entry.
func dosTime(t time.Time) (date, clock uint16) {
	date = uint16(t.Year()-1980)<<9 | uint16(t.Month())<<5 | uint16(t.Day())
	clock = uint16(t.Hour())<<11 | uint16(t.Minute())<<5 | uint16(t.Second()/2)
	return date, clock
}

// packODF makes the zip of an OpenDocument kind: mimetype first and stored, then the other parts in name order.
func packODF(d odfDoc) ([]byte, error) {
	mt := []byte(d.mime)
	parts := map[string]string{
		"content.xml":           odfContent(d.body),
		"styles.xml":            odfStyles(d.width, d.height, d.orient),
		"META-INF/manifest.xml": odfManifest(d.mime, []string{"content.xml", "styles.xml"}),
	}
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// CreateRaw writes the CRC and sizes in the local header, with no data descriptor, and the header has no extra field.
	date, clock := dosTime(zipTime)
	h := &zip.FileHeader{Name: "mimetype", Method: zip.Store, CRC32: crc32.ChecksumIEEE(mt),
		CompressedSize64: uint64(len(mt)), UncompressedSize64: uint64(len(mt)), ModifiedDate: date, ModifiedTime: clock}
	w, err := zw.CreateRaw(h)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(mt); err != nil {
		return nil, err
	}
	for _, name := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(parts[name])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
