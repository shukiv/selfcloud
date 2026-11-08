// SPDX-License-Identifier: AGPL-3.0-or-later

// Package blankdoc makes the blank Office documents that New creates (decision 91): an empty Word document, an empty
// workbook and a presentation with one empty slide, each as an OOXML package (ECMA-376, the .docx, .xlsx and .pptx
// formats). From decision 105 it also makes the OpenDocument kinds (odt, ods, odp, odg, in odf.go) and the empty text
// file (txt).
//
// The packages are built here, not stored as binary files. Each part is written out below as its XML text, and Build zips
// them in a fixed order with fixed timestamps, so the same kind is the same bytes every time and a reviewer reads the XML
// itself. Only the parts each format needs are included. Word needs the document part; a workbook needs a sheet and the
// workbook's relationship to it; a presentation needs a master, one layout, one theme and one slide, because an editor
// opens a deck with a slide to show. No styles, fonts or document properties are included: LibreOffice and Collabora
// fill in their own defaults. Each package was checked by unzipping it, parsing every part as XML, and converting it with
// LibreOffice (soffice --headless --convert-to pdf); see the evidence notes for the run.
package blankdoc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"sort"
	"time"
)

// The media types of the three formats, as the Office suites name them.
const (
	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	mimeXlsx = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimePptx = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)

// MIME is the media type of a kind, or "" for a kind this package does not make. The kinds are the OOXML ones (docx,
// xlsx, pptx), the OpenDocument ones (odt, ods, odp, odg) and txt.
func MIME(kind string) string {
	switch kind {
	case "docx":
		return mimeDocx
	case "xlsx":
		return mimeXlsx
	case "pptx":
		return mimePptx
	case "txt":
		return mimeTxt
	}
	if d, ok := odfDocs[kind]; ok {
		return d.mime
	}
	return ""
}

// zipTime is the modification time of every entry: fixed, so the bytes do not depend on when they were built.
var zipTime = time.Date(2026, time.November, 5, 0, 0, 0, 0, time.UTC)

const (
	xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	nsR     = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	nsA     = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
	nsP     = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
	relsNS  = `http://schemas.openxmlformats.org/package/2006/relationships`
	relDoc  = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/`
	ctypes  = `http://schemas.openxmlformats.org/package/2006/content-types`
	ctRels  = `application/vnd.openxmlformats-package.relationships+xml`
	ctXML   = `application/xml`
)

// rootRels points the package at its main part.
func rootRels(main string) string {
	return xmlHead + `<Relationships xmlns="` + relsNS + `">` +
		`<Relationship Id="rId1" Type="` + relDoc + `officeDocument" Target="` + main + `"/>` +
		`</Relationships>`
}

// contentTypes declares the defaults and the overrides of a package: the parts' media types by name.
func contentTypes(overrides ...[2]string) string {
	s := xmlHead + `<Types xmlns="` + ctypes + `">` +
		`<Default Extension="rels" ContentType="` + ctRels + `"/>` +
		`<Default Extension="xml" ContentType="` + ctXML + `"/>`
	for _, o := range overrides {
		s += `<Override PartName="/` + o[0] + `" ContentType="` + o[1] + `"/>`
	}
	return s + `</Types>`
}

// The Word document: one empty paragraph and the page setup Word expects at the end of the body.
var docx = map[string]string{
	"[Content_Types].xml": contentTypes(
		[2]string{"word/document.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"}),
	"_rels/.rels": rootRels("word/document.xml"),
	"word/document.xml": xmlHead + `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` + nsR + `>` +
		`<w:body><w:p/><w:sectPr><w:pgSz w:w="11906" w:h="16838"/>` +
		`<w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="708" w:footer="708" w:gutter="0"/>` +
		`</w:sectPr></w:body></w:document>`,
}

// The workbook: one sheet named Sheet1 with no cells. Excel and LibreOffice add their own styles when they save.
var xlsx = map[string]string{
	"[Content_Types].xml": contentTypes(
		[2]string{"xl/workbook.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"},
		[2]string{"xl/worksheets/sheet1.xml", "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"}),
	"_rels/.rels": rootRels("xl/workbook.xml"),
	"xl/workbook.xml": xmlHead + `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` + nsR + `>` +
		`<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
	"xl/_rels/workbook.xml.rels": xmlHead + `<Relationships xmlns="` + relsNS + `">` +
		`<Relationship Id="rId1" Type="` + relDoc + `worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
	"xl/worksheets/sheet1.xml": xmlHead + `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` + nsR + `>` +
		`<sheetData/></worksheet>`,
}

// The presentation: one master with one blank layout and one theme, and one empty slide on that layout. The theme is the
// Office theme's colours, fonts and format lists, in the schema's required order.
var pptx = map[string]string{
	"[Content_Types].xml": contentTypes(
		[2]string{"ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"},
		[2]string{"ppt/slideMasters/slideMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"},
		[2]string{"ppt/slideLayouts/slideLayout1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"},
		[2]string{"ppt/slides/slide1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"},
		[2]string{"ppt/theme/theme1.xml", "application/vnd.openxmlformats-officedocument.theme+xml"}),
	"_rels/.rels": rootRels("ppt/presentation.xml"),
	"ppt/presentation.xml": xmlHead + `<p:presentation ` + nsA + ` ` + nsR + ` ` + nsP + ` saveSubsetFonts="1">` +
		`<p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst>` +
		`<p:sldIdLst><p:sldId id="256" r:id="rId2"/></p:sldIdLst>` +
		`<p:sldSz cx="12192000" cy="6858000"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`,
	"ppt/_rels/presentation.xml.rels": xmlHead + `<Relationships xmlns="` + relsNS + `">` +
		`<Relationship Id="rId1" Type="` + relDoc + `slideMaster" Target="slideMasters/slideMaster1.xml"/>` +
		`<Relationship Id="rId2" Type="` + relDoc + `slide" Target="slides/slide1.xml"/>` +
		`<Relationship Id="rId3" Type="` + relDoc + `theme" Target="theme/theme1.xml"/></Relationships>`,
	"ppt/slideMasters/slideMaster1.xml": xmlHead + `<p:sldMaster ` + nsA + ` ` + nsR + ` ` + nsP + `>` +
		`<p:cSld><p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + emptyTree + `</p:spTree></p:cSld>` +
		`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" ` +
		`accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
		`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst></p:sldMaster>`,
	"ppt/slideMasters/_rels/slideMaster1.xml.rels": xmlHead + `<Relationships xmlns="` + relsNS + `">` +
		`<Relationship Id="rId1" Type="` + relDoc + `slideLayout" Target="../slideLayouts/slideLayout1.xml"/>` +
		`<Relationship Id="rId2" Type="` + relDoc + `theme" Target="../theme/theme1.xml"/></Relationships>`,
	"ppt/slideLayouts/slideLayout1.xml": xmlHead + `<p:sldLayout ` + nsA + ` ` + nsR + ` ` + nsP + ` type="blank" preserve="1">` +
		`<p:cSld name="Blank"><p:spTree>` + emptyTree + `</p:spTree></p:cSld>` +
		`<p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`,
	"ppt/slideLayouts/_rels/slideLayout1.xml.rels": xmlHead + `<Relationships xmlns="` + relsNS + `">` +
		`<Relationship Id="rId1" Type="` + relDoc + `slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>`,
	"ppt/slides/slide1.xml": xmlHead + `<p:sld ` + nsA + ` ` + nsR + ` ` + nsP + `>` +
		`<p:cSld><p:spTree>` + emptyTree + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`,
	"ppt/slides/_rels/slide1.xml.rels": xmlHead + `<Relationships xmlns="` + relsNS + `">` +
		`<Relationship Id="rId1" Type="` + relDoc + `slideLayout" Target="../slideLayouts/slideLayout1.xml"/></Relationships>`,
	"ppt/theme/theme1.xml": theme,
}

// emptyTree is a shape tree with no shapes: its group properties only.
const emptyTree = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr>` +
	`<p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

// theme is the Office theme: the twelve colours, the two fonts, and the three of each format list the schema requires.
const theme = xmlHead + `<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office Theme"><a:themeElements>` +
	`<a:clrScheme name="Office">` +
	`<a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>` +
	`<a:dk2><a:srgbClr val="44546A"/></a:dk2><a:lt2><a:srgbClr val="E7E6E6"/></a:lt2>` +
	`<a:accent1><a:srgbClr val="4472C4"/></a:accent1><a:accent2><a:srgbClr val="ED7D31"/></a:accent2>` +
	`<a:accent3><a:srgbClr val="A5A5A5"/></a:accent3><a:accent4><a:srgbClr val="FFC000"/></a:accent4>` +
	`<a:accent5><a:srgbClr val="5B9BD5"/></a:accent5><a:accent6><a:srgbClr val="70AD47"/></a:accent6>` +
	`<a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme>` +
	`<a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>` +
	`<a:minorFont><a:latin typeface="Calibri"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>` +
	`<a:fmtScheme name="Office">` +
	`<a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst>` +
	`<a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>` +
	`<a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>` +
	`<a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln></a:lnStyleLst>` +
	`<a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle>` +
	`<a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst>` +
	`<a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill>` +
	`<a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme>` +
	`</a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`

// kinds maps each kind to its parts. The order of a package is the order of its names, with [Content_Types].xml first.
var kinds = map[string]map[string]string{"docx": docx, "xlsx": xlsx, "pptx": pptx}

// Build returns the blank document of kind as a zip package, or an error for a kind this package does not make. The
// result is the same bytes on every call. txt is an empty file, not a package: its result is no bytes.
func Build(kind string) ([]byte, error) {
	if kind == "txt" {
		return []byte{}, nil
	}
	if d, ok := odfDocs[kind]; ok {
		return packODF(d)
	}
	parts, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("blankdoc: no blank document of kind %q", kind)
	}
	return Pack(parts)
}

// Parts returns a copy of the parts of an OOXML kind (docx, xlsx or pptx), by name, for a caller that writes its own text
// into them: the built-in templates do. It is nil for a kind this package does not make as OOXML.
func Parts(kind string) map[string]string {
	src, ok := kinds[kind]
	if !ok {
		return nil
	}
	out := make(map[string]string, len(src))
	for name, text := range src {
		out[name] = text
	}
	return out
}

// Pack zips parts in the order Build uses: [Content_Types].xml first, then the other parts by name, each with the fixed
// timestamp. The same parts are the same bytes every time. A set with no [Content_Types].xml is refused.
func Pack(parts map[string]string) ([]byte, error) {
	if _, ok := parts["[Content_Types].xml"]; !ok {
		return nil, fmt.Errorf("blankdoc: the package has no [Content_Types].xml")
	}
	names := make([]string, 0, len(parts))
	for name := range parts {
		if name != "[Content_Types].xml" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = append([]string{"[Content_Types].xml"}, names...)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime}
		w, err := zw.CreateHeader(h)
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
